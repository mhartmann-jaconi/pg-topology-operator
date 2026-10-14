# Changelog

All notable changes to this project are documented in this file.

This repository contains two independently versioned release artifacts:

- **Application:** Go application published as a Docker image.
- **Helm chart:** Kubernetes deployment chart published as a `.tgz` package.

Both components follow [Semantic Versioning](https://semver.org/).

## Application (Docker Image)

### [v0.2.0] - 2026-10-04

#### Changed 

- Upgraded to golang 1.27 version

### [v0.11.1] - 2026-07-01

First main release.

---

## Helm Chart

### [0.6.0] - 2026-10-04

#### Changed 

- Uses operator version v0.2.0

### [0.5.6] - 2026-07-01

First main release.

---

## Release Tags

Application releases use Git tags in the format `app/vX.Y.Z`.

Helm chart releases use Git tags in the format `chart/vX.Y.Z`.

Each release is independently versioned and published.