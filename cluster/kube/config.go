package kube

const (
	issuer        = "issuer"
	clusterIssuer = "cluster-issuer"
)

type ClientConfig struct {
	Ssl Ssl
}

type Ssl struct {
	IssuerType string
	IssuerName string
}
