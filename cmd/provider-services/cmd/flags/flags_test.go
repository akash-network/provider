package flags

import "testing"

func TestValidateProxyBufferSize(t *testing.T) {
	valid := []string{"", "16k", "16K", "1m", "1M", "512k", "1024", "8k"}
	for _, v := range valid {
		if err := ValidateProxyBufferSize(v); err != nil {
			t.Errorf("ValidateProxyBufferSize(%q) = %v, want nil", v, err)
		}
	}

	// "16kb" is the operator typo from the review; the rest are other bad forms.
	invalid := []string{"16kb", "16g", "16G", "abc", "0", "0k", "-5", "16 k", "1.5m", "k", "16m2"}
	for _, v := range invalid {
		if err := ValidateProxyBufferSize(v); err == nil {
			t.Errorf("ValidateProxyBufferSize(%q) = nil, want error", v)
		}
	}
}
