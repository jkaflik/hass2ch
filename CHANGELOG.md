# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Prometheus metrics endpoint for observability
- Comprehensive metrics for pipeline monitoring
- Helm chart for Kubernetes deployment
- Retry mechanism for ClickHouse operations
- Multi-architecture Docker images (amd64, arm64)
- AlertManager rules for common failure scenarios
- Grafana dashboards for visualization
- Detailed documentation

### Changed
- Refactored ClickHouse client for better error handling
- Improved batch processing with metrics
- Enhanced logging with structured data

### Fixed
- Potential data loss during ClickHouse outages
- Connection handling for Home Assistant

## [0.1.0] - 2023-06-01

### Added
- Initial release
- Basic Home Assistant to ClickHouse pipeline
- WebSocket connection to Home Assistant
- Event filtering and processing
- Simple batching mechanism
- ClickHouse client for data storage