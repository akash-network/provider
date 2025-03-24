# Contributing to Akash Network Provider

Thank you for your interest in contributing to the Akash Network Provider! This document provides guidelines and instructions for contributing to the project.

## Software Requirements

Before you begin, ensure you have the following software installed with the required versions:

### Core Requirements
- Go 1.21.0 or later
- GNU Make 4.0 or later
- Bash 4.0 or later
- direnv 2.32.x or later
- wget
- realpath

### Additional Required Tools
- unzip
- curl
- npm
- jq
- readlink
- git

### Platform-Specific Requirements

#### macOS
- Homebrew (https://brew.sh)
- gnu-getopt (`brew install gnu-getopt`)

### Version Verification
You can verify your versions using:
```bash
# Go version
go version

# Make version
make --version

# Bash version
bash --version

# Direnv version
direnv version
```

### Installing/Upgrading Requirements

#### GNU Make on macOS
The default make version on macOS is usually outdated. To install the latest GNU Make:

```bash
# Install using Homebrew
brew install make

# The new GNU Make will be installed as 'gmake'
# Add this to your ~/.zshrc or ~/.bashrc to use it as 'make':
export PATH="/usr/local/opt/make/libexec/gnubin:$PATH"
```

#### Direnv Setup
Direnv is required for managing environment variables. To set it up:

1. Install direnv:
   ```bash
   # On macOS
   brew install direnv

   # On Linux
   # Follow instructions at https://direnv.net/docs/installation.html
   ```

2. Add direnv hook to your shell:
   ```bash
   # For zsh (add to ~/.zshrc):
   eval "$(direnv hook zsh)"

   # For bash (add to ~/.bashrc):
   eval "$(direnv hook bash)"
   ```

3. After cloning the repository, run:
   ```bash
   direnv allow
   ```

#### Installing Other Dependencies

On macOS:
```bash
brew install wget jq npm unzip curl gnu-getopt
```

On Ubuntu/Debian:
```bash
sudo apt-get update
sudo apt-get install wget jq npm unzip curl getopt
```

## Project Overview

The Akash Network Provider is a Go-based implementation that enables providers to participate in the Akash Network marketplace. It handles deployment management, resource allocation, and bid processing for the decentralized cloud computing platform.

## Development Environment Setup

1. Ensure you have Go 1.21 or later installed
2. Fork and clone the repository:
   ```bash
   git clone https://github.com/akash-network/provider.git
   cd provider
   ```
3. Install required tools:
   ```bash
   make cache
   ```
4. Set up your development environment:
   ```bash
   make setup
   ```
5. Configure your environment variables:
   ```bash
   cp .env.example .env
   # Edit .env with your configuration
   ```

## Project Structure

- `cmd/` - Main application entry points
- `pkg/` - Core packages and shared functionality
- `operator/` - Provider operator implementation
- `bidengine/` - Bidding engine implementation
- `gateway/` - API gateway implementation
- `cluster/` - Kubernetes cluster management
- `manifest/` - Deployment manifest handling
- `testutil/` - Testing utilities
- `integration/` - Integration tests

## Code Style and Standards

- Follow the [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Use `gofmt` to format your code
- Run `make lint` to check for code style issues
- Ensure all tests pass with `make test`
- Write unit tests for new functionality
- Update documentation as needed
- Use interfaces for better testability and modularity
- Follow the existing patterns in the codebase

## Development Workflow

1. Create a new branch for your feature/fix:
   ```bash
   git checkout -b feature/your-feature-name
   # or
   git checkout -b fix/your-fix-name
   ```

2. Make your changes following the project's architecture and patterns

3. Run tests and linting:
   ```bash
   make test
   make lint
   ```

4. Commit your changes with clear, descriptive messages:
   ```bash
   git commit -m "feat: add new feature X"
   # or
   git commit -m "fix: resolve issue Y"
   ```

5. Push to your fork and create a Pull Request

## Testing

- Write unit tests for new functionality
- Run integration tests when applicable:
  ```bash
  make test-integration
  ```
- Ensure test coverage meets project standards
- Use the testutil package for common test utilities
- Mock external dependencies using the mocks package
- Test error cases and edge conditions

## Documentation

- Update relevant documentation in `_docs/` directory
- Add comments for complex logic
- Update README.md if needed
- Document any new configuration options
- Include examples for new features
- Update API documentation if applicable

## Release Process

1. Follow semantic versioning
2. Update version information in the version package
3. Update CHANGELOG.md using the provided tools:
   ```bash
   make changelog
   ```
4. Ensure all tests pass before creating a release
5. Create a release tag:
   ```bash
   git tag -a vX.Y.Z -m "Release vX.Y.Z"
   git push origin vX.Y.Z
   ```

## Getting Help

- Join our [Discord community](https://discord.gg/akash)
- Check existing issues and pull requests
- Ask questions in the community channels
- Review the project documentation in `_docs/` directory

## Code of Conduct

Please note that this project is released with a [Contributor Code of Conduct](CODE_OF_CONDUCT.md). By participating in this project you agree to abide by its terms.

## License

By contributing, you agree that your contributions will be licensed under the project's [Apache 2.0 License](LICENSE).

Thank you for contributing to the Akash Network Provider! 