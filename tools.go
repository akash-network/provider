//go:build tools

package tools

// nolint
import (
	_ "github.com/vektra/mockery/v2"
	_ "k8s.io/code-generator"
	_ "sigs.k8s.io/kind"
)
