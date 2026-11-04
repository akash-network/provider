package sign

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Command delegates signing to an external program: the payload goes to its
// stdin and the raw signature is read from its stdout.
//
// This is what keeps custody out of the trust model. A key service CLI, an HSM
// utility or a hardware-token tool all reach this interface without a change
// here, so moving key material between custody domains is configuration rather
// than a code change, and leaves the keypair, the trust root and every
// published document untouched.
type Command struct {
	id   string
	name string
	args []string
}

func NewCommand(id, name string, args ...string) (*Command, error) {
	if id == "" {
		return nil, fmt.Errorf("sign: key id is empty")
	}
	if name == "" {
		return nil, fmt.Errorf("sign: key %q has no command", id)
	}
	return &Command{id: id, name: name, args: append([]string(nil), args...)}, nil
}

func (c *Command) KeyID() string { return c.id }

func (c *Command) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.name, c.args...)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%s: %w: %s", c.name, err, bytes.TrimSpace(stderr.Bytes()))
		}
		return nil, fmt.Errorf("%s: %w", c.name, err)
	}

	return stdout.Bytes(), nil
}
