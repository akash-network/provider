package builder

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ServiceCredentials interface {
	NS() string
	Name() string
	Create() (*corev1.Secret, error)
	Update(obj *corev1.Secret) (*corev1.Secret, error)
}

type serviceCredentials struct {
	ns          string
	serviceName string
	credentials *mani.ServiceImageCredentials
	// TODO: labels for deleting
	// labels      map[string]string
}

func NewServiceCredentials(ns string, serviceName string, credentials *mani.ServiceImageCredentials) ServiceCredentials {
	return serviceCredentials{
		ns:          ns,
		serviceName: serviceName,
		credentials: credentials,
	}
}

func (b serviceCredentials) NS() string {
	return b.ns
}

func (b serviceCredentials) Name() string {
	return fmt.Sprintf("docker-creds-%v", b.serviceName)
}

func (b serviceCredentials) Create() (*corev1.Secret, error) {
	// see https://github.com/kubernetes/kubectl/blob/master/pkg/cmd/create/create_secret_docker.go#L280-L298

	data, err := b.encodeSecret()
	if err != nil {
		return nil, err
	}

	obj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: b.ns,
			Name:      b.Name(),
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

	obj.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: data,
	}
	obj.Type = corev1.SecretTypeDockerConfigJson
	return obj, nil
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
		Username: b.credentials.Username,
		Password: b.credentials.Password,
		Email:    b.credentials.Email,
		Auth:     encodeAuth(b.credentials.Username, b.credentials.Password),
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
