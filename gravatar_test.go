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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestComputeAuthorID(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{
			email:    "Piero.Proietti@Gmail.com ",
			expected: func() string {
				h := sha256.Sum256([]byte("piero.proietti@gmail.com"))
				return hex.EncodeToString(h[:])
			}(),
		},
		{
			email:    "",
			expected: "",
		},
		{
			email:    "   ",
			expected: "",
		},
	}

	for _, tc := range tests {
		got := ComputeAuthorID(tc.email)
		if got != tc.expected {
			t.Errorf("ComputeAuthorID(%q) = %q, want %q", tc.email, got, tc.expected)
		}
	}
}

func TestGravatarResolutionAndCache(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/found.json") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"entry": [
					{
						"displayName": "Eggs Artisan",
						"preferredUsername": "eggsartisan"
					}
				]
			}`))
			return
		}
		if strings.HasPrefix(path, "/only-username.json") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"entry": [
					{
						"preferredUsername": "usernameonly"
					}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	client := NewGravatarClient(1 * time.Hour)
	client.SetBaseURL(mockServer.URL)
	client.SetHTTPClient(mockServer.Client())

	// 1. Resolve author with displayName
	nick1, avatar1, prof1 := client.Resolve("found")
	if nick1 != "Eggs Artisan" {
		t.Errorf("expected nickname 'Eggs Artisan', got %q", nick1)
	}
	if avatar1 != "https://www.gravatar.com/avatar/found?s=96&d=identicon" {
		t.Errorf("unexpected avatar URL: %s", avatar1)
	}
	if prof1 != "https://gravatar.com/found" {
		t.Errorf("unexpected profile URL: %s", prof1)
	}

	// 2. Resolve author with preferredUsername fallback
	nick2, _, _ := client.Resolve("only-username")
	if nick2 != "usernameonly" {
		t.Errorf("expected nickname 'usernameonly', got %q", nick2)
	}

	// 3. Fallback when 404 (non-existent profile on Gravatar)
	authorID := "abcdef1234567890"
	nick3, avatar3, prof3 := client.Resolve(authorID)
	if nick3 != "author-abcdef12" {
		t.Errorf("expected fallback nickname 'author-abcdef12', got %q", nick3)
	}
	if avatar3 != fmt.Sprintf("https://www.gravatar.com/avatar/%s?s=96&d=identicon", authorID) {
		t.Errorf("unexpected avatar URL: %s", avatar3)
	}
	if prof3 != fmt.Sprintf("https://gravatar.com/%s", authorID) {
		t.Errorf("unexpected profile URL: %s", prof3)
	}

	// 4. Verify cache hit (change base URL to invalid, should still return cached result)
	client.SetBaseURL("http://127.0.0.1:0")
	nick1Cached, _, _ := client.Resolve("found")
	if nick1Cached != "Eggs Artisan" {
		t.Errorf("expected cached nickname 'Eggs Artisan', got %q", nick1Cached)
	}
}
