PACKAGE  := penguins-census
VERSION  ?= 0.2.2-1
ARCH     ?= amd64
DEB_NAME := $(PACKAGE)_$(VERSION)_$(ARCH).deb
STAGE    := build/deb/$(PACKAGE)_$(VERSION)_$(ARCH)

.PHONY: all build test deb clean

all: build

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -ldflags="-s -w" -o $(PACKAGE) .

test:
	go test -v -race ./...

deb: build
	rm -rf $(STAGE)
	install -d -m 0755 $(STAGE)/DEBIAN
	install -d -m 0755 $(STAGE)/usr/bin
	install -d -m 0755 $(STAGE)/usr/sbin
	install -d -m 0755 $(STAGE)/lib/systemd/system
	install -d -m 0750 $(STAGE)/var/lib/penguins-census
	install -d -m 0750 $(STAGE)/var/lib/penguins-census/community

	install -m 0755 $(PACKAGE) $(STAGE)/usr/bin/$(PACKAGE)
	install -m 0755 packaging/penguins-census-update-geoip $(STAGE)/usr/sbin/penguins-census-update-geoip
	install -m 0644 packaging/penguins-census.service $(STAGE)/lib/systemd/system/penguins-census.service

	install -m 0644 packaging/DEBIAN/control $(STAGE)/DEBIAN/control
	sed -i "s/^Version:.*/Version: $(VERSION)/" $(STAGE)/DEBIAN/control
	sed -i "s/^Architecture:.*/Architecture: $(ARCH)/" $(STAGE)/DEBIAN/control
	install -m 0755 packaging/DEBIAN/postinst $(STAGE)/DEBIAN/postinst
	install -m 0755 packaging/DEBIAN/prerm $(STAGE)/DEBIAN/prerm

	dpkg-deb --build --root-owner-group $(STAGE) $(DEB_NAME)

clean:
	rm -rf build $(PACKAGE) *.deb
