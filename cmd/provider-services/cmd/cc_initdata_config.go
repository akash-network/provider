package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/akash-network/provider/cluster/kube/builder"
)

const ccKBSMaxCertificateBytes = 256 * 1024

func loadCCInitDataSettings(
	kbsURL,
	kbsCertificatePath,
	imageSecurityPolicyURI,
	agentPolicyPath string,
) (*builder.CCInitDataSettings, error) {
	configured := kbsURL != "" || kbsCertificatePath != "" || imageSecurityPolicyURI != "" || agentPolicyPath != ""
	if !configured {
		return nil, nil
	}
	if kbsURL == "" || kbsCertificatePath == "" || imageSecurityPolicyURI == "" || agentPolicyPath == "" {
		return nil, fmt.Errorf("cc-kbs-url, cc-kbs-cert-file, cc-image-security-policy-uri, and cc-agent-policy-file must be configured together")
	}
	if !filepath.IsAbs(kbsCertificatePath) || !filepath.IsAbs(agentPolicyPath) {
		return nil, fmt.Errorf("confidential-compute certificate and policy paths must be absolute")
	}

	certificate, err := os.ReadFile(kbsCertificatePath)
	if err != nil {
		return nil, fmt.Errorf("read public KBS certificate: %w", err)
	}
	if len(certificate) > ccKBSMaxCertificateBytes {
		return nil, fmt.Errorf("public KBS certificate chain exceeds %d bytes", ccKBSMaxCertificateBytes)
	}
	agentPolicy, err := os.ReadFile(agentPolicyPath)
	if err != nil {
		return nil, fmt.Errorf("read confidential guest agent policy: %w", err)
	}

	settings := &builder.CCInitDataSettings{
		KBSURL:                 kbsURL,
		KBSCertificate:         string(certificate),
		ImageSecurityPolicyURI: imageSecurityPolicyURI,
		AgentPolicy:            string(agentPolicy),
	}
	if err := builder.ValidateSettings(builder.Settings{CCInitData: settings}); err != nil {
		return nil, err
	}

	return settings, nil
}
