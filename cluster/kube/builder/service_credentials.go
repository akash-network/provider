package builder

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mani "pkg.akt.dev/go/manifest/v2beta3"
)

type ServiceCredentials interface {
	workloadBase
	Create() (*corev1.Secret, error)
	Update(obj *corev1.Secret) (*corev1.Secret, error)
}

type serviceCredentials struct {
	*Workload
	credentials *mani.ImageCredentials
}

func NewServiceCredentials(workload *Workload, credentials *mani.ImageCredentials) ServiceCredentials {
	return &serviceCredentials{
		Workload:    workload,
		credentials: credentials,
	}
}

func (b serviceCredentials) Name() string {
	svc := &b.deployment.ManifestGroup().Services[b.serviceIdx]
	return fmt.Sprintf("docker-creds-%v", svc.Name)
}

func (b serviceCredentials) Create() (*corev1.Secret, error) {
	// see https://github.com/kubernetes/kubectl/blob/master/pkg/cmd/create/create_secret_docker.go#L280-L298
	data, err := b.encodeSecret()
	if err != nil {
		return nil, err
	}

	obj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: b.NS(),
			Name:      b.Name(),
			Labels:    b.labels(),
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: data,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	return obj, nil
}

func (b serviceCredentials) Update(obj *corev1.Secret) (*corev1.Secret, error) {
	// see https://github.com/kubernetes/kubectl/blob/master/pkg/cmd/create/create_secret_docker.go#L280-L298
	data, err := b.encodeSecret()
	if err != nil {
		return nil, err
	}

	uobj := obj.DeepCopy()

	uobj.Labels = updateAkashLabels(obj.Labels, b.labels())

	uobj.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: data,
	}
	uobj.Type = corev1.SecretTypeDockerConfigJson

	return uobj, nil
}

type dockerCredentialsEntry struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Email    string `json:"email,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

type dockerCredentials struct {
	Auths map[string]dockerCredentialsEntry `json:"auths"`
}

func (b serviceCredentials) encodeSecret() ([]byte, error) {
	entry := dockerCredentialsEntry{
		Username: strings.TrimSpace(b.credentials.Username),
		Password: strings.TrimSpace(b.credentials.Password),
		Email:    strings.TrimSpace(b.credentials.Email),
		Auth:     encodeAuth(strings.TrimSpace(b.credentials.Username), strings.TrimSpace(b.credentials.Password)),
	}
	creds := dockerCredentials{
		Auths: map[string]dockerCredentialsEntry{
			b.credentials.Host: entry,
		},
	}

	return json.Marshal(creds)
}

func encodeAuth(username, password string) string {
	value := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(value))
}
