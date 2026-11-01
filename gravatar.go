/*
 * penguins-census - Copyright (C) 2026-2026 Piero Proietti
 * Author: Piero Proietti <piero.proietti@gmail.com>
 * https://penguins-eggs.net
 *
 * This file is part of penguins-census.
 * Released under the GNU GPL v3 license.
 */

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type gravatarCacheItem struct {
	displayName string
	cachedAt    time.Time
}

type GravatarClient struct {
	mu         sync.RWMutex
	cache      map[string]gravatarCacheItem
	ttl        time.Duration
	httpClient *http.Client
	baseURL    string
}

func NewGravatarClient(ttl time.Duration) *GravatarClient {
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	return &GravatarClient{
		cache: make(map[string]gravatarCacheItem),
		ttl:   ttl,
		httpClient: &http.Client{
			Timeout: 4 * time.Second,
		},
		baseURL: "https://gravatar.com",
	}
}

func (g *GravatarClient) SetBaseURL(url string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.baseURL = strings.TrimSuffix(url, "/")
}

func (g *GravatarClient) SetHTTPClient(client *http.Client) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.httpClient = client
}

func (g *GravatarClient) SetCached(authorID, displayName string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cache[strings.ToLower(strings.TrimSpace(authorID))] = gravatarCacheItem{
		displayName: displayName,
		cachedAt:    time.Now(),
	}
}

// ComputeAuthorID normalizes the email (lowercase and trimmed) and returns its hexadecimal SHA-256 hash.
func ComputeAuthorID(email string) string {
	cleaned := strings.ToLower(strings.TrimSpace(email))
	if cleaned == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(cleaned))
	return hex.EncodeToString(hash[:])
}

func (g *GravatarClient) Resolve(authorID string) (displayName, avatarURL, profileURL string) {
	authorID = strings.ToLower(strings.TrimSpace(authorID))
	fallbackNick := "author-"
	if len(authorID) >= 8 {
		fallbackNick += authorID[:8]
	} else if authorID != "" {
		fallbackNick += authorID
	} else {
		fallbackNick = "author"
	}

	avatarURL = fmt.Sprintf("https://www.gravatar.com/avatar/%s?s=96&d=identicon", authorID)
	profileURL = fmt.Sprintf("https://gravatar.com/%s", authorID)

	if authorID == "" {
		return fallbackNick, avatarURL, profileURL
	}

	g.mu.RLock()
	item, found := g.cache[authorID]
	baseURL := g.baseURL
	client := g.httpClient
	ttl := g.ttl
	g.mu.RUnlock()

	if found && time.Since(item.cachedAt) < ttl {
		return item.displayName, avatarURL, profileURL
	}

	resolvedName := fallbackNick

	reqURL := fmt.Sprintf("%s/%s.json", strings.TrimSuffix(baseURL, "/"), authorID)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err == nil {
		req.Header.Set("User-Agent", "penguins-census/1.0")
		resp, doErr := client.Do(req)
		if doErr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var gResp struct {
					Entry []struct {
						DisplayName       string `json:"displayName"`
						PreferredUsername string `json:"preferredUsername"`
					} `json:"entry"`
					DisplayName       string `json:"display_name"`
					PreferredUsername string `json:"preferred_username"`
				}
				if jsonErr := json.NewDecoder(resp.Body).Decode(&gResp); jsonErr == nil {
					candidate := ""
					if len(gResp.Entry) > 0 {
						candidate = strings.TrimSpace(gResp.Entry[0].DisplayName)
						if candidate == "" {
							candidate = strings.TrimSpace(gResp.Entry[0].PreferredUsername)
						}
					}
					if candidate == "" {
						candidate = strings.TrimSpace(gResp.DisplayName)
					}
					if candidate == "" {
						candidate = strings.TrimSpace(gResp.PreferredUsername)
					}
					if candidate != "" {
						resolvedName = candidate
					}
				}
			}
		}
	}

	g.mu.Lock()
	g.cache[authorID] = gravatarCacheItem{
		displayName: resolvedName,
		cachedAt:    time.Now(),
	}
	g.mu.Unlock()

	return resolvedName, avatarURL, profileURL
}
