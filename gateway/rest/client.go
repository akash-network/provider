package rest

// import (
// 	"bytes"
// 	"context"
// 	"crypto"
// 	"crypto/tls"
// 	"crypto/x509"
// 	"encoding/json"
// 	"encoding/pem"
// 	"fmt"
// 	"io"
// 	"math/big"
// 	"net/http"
// 	"net/url"
// 	"strconv"
// 	"strings"
// 	"time"
//
// 	"github.com/golang-jwt/jwt/v5"
// 	"github.com/gorilla/websocket"
// 	"github.com/pkg/errors"
// 	"k8s.io/client-go/tools/remotecommand"
//
// 	sdk "github.com/cosmos/cosmos-sdk/types"
//
// 	manifest "pkg.akt.dev/go/manifest/v2beta2"
// 	ctypes "pkg.akt.dev/go/node/cert/v1beta3"
// 	dtypes "pkg.akt.dev/go/node/deployment/v1beta3"
// 	mtypes "pkg.akt.dev/go/node/market/v1beta4"
// 	ajwt "pkg.akt.dev/go/util/jwt"
// 	atls "pkg.akt.dev/go/util/tls"
//
// 	"github.com/akash-network/provider"
// 	cltypes "github.com/akash-network/provider/cluster/types/v1beta3"
// )
//
// const (
// 	schemeWSS   = "wss"
// 	schemeHTTPS = "https"
// )
//
// var (
// 	ErrNotInitialized = errors.New("rest: not initialized")
// )
//
// // Client defines the methods available for connecting to the gateway server.
// type Client interface {
// 	Status(ctx context.Context) (*provider.Status, error)
// 	Validate(ctx context.Context, gspec dtypes.GroupSpec) (provider.ValidateGroupSpecResult, error)
// 	SubmitManifest(ctx context.Context, dseq uint64, mani manifest.Manifest) error
// 	GetManifest(ctx context.Context, id mtypes.LeaseID) (manifest.Manifest, error)
// 	LeaseStatus(ctx context.Context, id mtypes.LeaseID) (LeaseStatus, error)
// 	LeaseEvents(ctx context.Context, id mtypes.LeaseID, services string, follow bool) (*LeaseKubeEvents, error)
// 	LeaseLogs(ctx context.Context, id mtypes.LeaseID, services string, follow bool, tailLines int64) (*ServiceLogs, error)
// 	ServiceStatus(ctx context.Context, id mtypes.LeaseID, service string) (*cltypes.ServiceStatus, error)
// 	LeaseShell(ctx context.Context, id mtypes.LeaseID, service string, podIndex uint, cmd []string,
// 		stdin io.Reader,
// 		stdout io.Writer,
// 		stderr io.Writer,
// 		tty bool,
// 		tsq <-chan remotecommand.TerminalSize) error
// 	MigrateHostnames(ctx context.Context, hostnames []string, dseq uint64, gseq uint32) error
// 	MigrateEndpoints(ctx context.Context, endpoints []string, dseq uint64, gseq uint32) error
// }
//
// type LeaseKubeEvent struct {
// 	Action  string `json:"action"`
// 	Message string `json:"message"`
// }
//
// type ServiceLogMessage struct {
// 	Name    string `json:"name"`
// 	Message string `json:"message"`
// }
//
// type LeaseKubeEvents struct {
// 	Stream  <-chan cltypes.LeaseEvent
// 	OnClose <-chan string
// }
//
// type ServiceLogs struct {
// 	Stream  <-chan ServiceLogMessage
// 	OnClose <-chan string
// }
//
// type httpClient interface {
// 	Do(*http.Request) (*http.Response, error)
// }
//
// type client struct {
// 	ctx     context.Context
// 	host    *url.URL
// 	addr    sdk.Address
// 	cclient ctypes.QueryClient
// 	tlsCfg  *tls.Config
// 	certs   []tls.Certificate
// 	signer  ajwt.SignerI
// }
//
// type ReqClient struct {
// 	ctx      context.Context
// 	host     *url.URL
// 	hclient  httpClient
// 	wsclient *websocket.Dialer
// 	addr     sdk.Address
// 	cclient  ctypes.QueryClient
// }
//
// type clientOptions struct {
// 	certs  []tls.Certificate
// 	signer ajwt.SignerI
// 	token  string
// }
//
// type ClientOption func(options *clientOptions) error
//
// func WithCerts(certs []tls.Certificate) ClientOption {
// 	return func(options *clientOptions) error {
// 		options.certs = certs
//
// 		return nil
// 	}
// }
//
// func WithJWTSigner(val ajwt.SignerI) ClientOption {
// 	return func(options *clientOptions) error {
// 		options.signer = val
// 		return nil
// 	}
// }
//
// func WithToken(val string) ClientOption {
// 	return func(options *clientOptions) error {
// 		options.token = val
// 		return nil
// 	}
// }
//
// // NewClient returns a new Client
// // func NewClient(ctx context.Context, qclient aclient.QueryClient, addr sdk.Address, opts ...ClientOption) (Client, error) {
// // 	cOpts := &clientOptions{}
// //
// // 	for _, opt := range opts {
// // 		err := opt(cOpts)
// // 		if err != nil {
// // 			return nil, err
// // 		}
// // 	}
// //
// // 	res, err := qclient.Provider(ctx, &ptypes.QueryProviderRequest{Owner: addr.String()})
// // 	if err != nil {
// // 		return nil, err
// // 	}
// //
// // 	uri, err := url.Parse(res.Provider.HostURI)
// // 	if err != nil {
// // 		return nil, err
// // 	}
// //
// // 	certPool, err := x509.SystemCertPool()
// // 	if err != nil {
// // 		return nil, err
// // 	}
// //
// // 	cl := &client{
// // 		ctx:     ctx,
// // 		host:    uri,
// // 		addr:    addr,
// // 		cclient: qclient,
// // 	}
// //
// // 	cl.tlsCfg = &tls.Config{
// // 		InsecureSkipVerify:    true, // nolint: gosec
// // 		VerifyPeerCertificate: cl.verifyPeerCertificate,
// // 		MinVersion:            tls.VersionTLS13,
// // 		RootCAs:               certPool,
// // 	}
// //
// // 	if len(cOpts.certs) > 0 {
// // 		cl.tlsCfg.Certificates = cOpts.certs
// // 	} else if cOpts.signer != nil {
// // 		// must use Hostname rather than Host field as a certificate is issued for host without port
// // 		cl.tlsCfg.ServerName = uri.Host
// // 		cl.signer = cOpts.signer
// // 	}
// //
// // 	return cl, nil
// // }
//
// func (c *client) verifyPeerCertificate(certificates [][]byte, _ [][]*x509.Certificate) error {
// 	peerCerts := make([]*x509.Certificate, 0, len(certificates))
//
// 	for idx := range certificates {
// 		cert, err := x509.ParseCertificate(certificates[idx])
// 		if err != nil {
// 			return err
// 		}
//
// 		peerCerts = append(peerCerts, cert)
// 	}
//
// 	if len(peerCerts) == 0 {
// 		return atls.CertificateInvalidError{Reason: atls.EmptyPeerCertificate}
// 	}
//
// 	// if the server provides just 1 certificate, it is most likely then not it is mTLS
// 	if len(peerCerts) == 1 {
// 		cert := peerCerts[0]
// 		// validation
// 		var owner sdk.Address
// 		var err error
//
// 		if owner, err = sdk.AccAddressFromBech32(cert.Subject.CommonName); err != nil {
// 			return fmt.Errorf("%w: (%w)", atls.CertificateInvalidError{Cert: cert, Reason: atls.EmptyPeerCertificate}, err)
// 		}
//
// 		// 1. CommonName in issuer and Subject must match and be as Bech32 format
// 		if cert.Subject.CommonName != cert.Issuer.CommonName {
// 			return fmt.Errorf("%w: (%w)", atls.CertificateInvalidError{Cert: cert, Reason: atls.InvalidCN}, err)
// 		}
//
// 		// 2. serial number must be in
// 		if cert.SerialNumber == nil {
// 			return fmt.Errorf("%w: (%w)", atls.CertificateInvalidError{Cert: cert, Reason: atls.InvalidSN}, err)
// 		}
//
// 		// 3. look up the certificate on the chain
// 		onChainCert, _, err := c.GetAccountCertificate(c.ctx, owner, cert.SerialNumber)
// 		if err != nil {
// 			return fmt.Errorf("%w: (%w)", atls.CertificateInvalidError{Cert: cert, Reason: atls.Expired}, err)
// 		}
//
// 		c.tlsCfg.RootCAs.AddCert(onChainCert)
// 	}
//
// 	opts := x509.VerifyOptions{
// 		Roots:                     c.tlsCfg.RootCAs,
// 		CurrentTime:               time.Now(),
// 		KeyUsages:                 []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
// 		MaxConstraintComparisions: 0,
// 	}
//
// 	for _, cert := range peerCerts {
// 		if _, err := cert.Verify(opts); err != nil {
// 			return fmt.Errorf("%w: (%w)", atls.CertificateInvalidError{Cert: cert, Reason: atls.Verify}, err)
// 		}
// 	}
//
// 	return nil
// }
//
// func (c *client) GetAccountCertificate(ctx context.Context, owner sdk.Address, serial *big.Int) (*x509.Certificate, crypto.PublicKey, error) {
// 	cresp, err := c.cclient.Certificates(ctx, &ctypes.QueryCertificatesRequest{
// 		Filter: ctypes.CertificateFilter{
// 			Owner:  owner.String(),
// 			Serial: serial.String(),
// 			State:  ctypes.CertificateValid.String(),
// 		},
// 	})
// 	if err != nil {
// 		return nil, nil, err
// 	}
//
// 	certData := cresp.Certificates[0]
//
// 	blk, rest := pem.Decode(certData.Certificate.Cert)
// 	if blk == nil || len(rest) > 0 {
// 		return nil, nil, ctypes.ErrInvalidCertificateValue
// 	} else if blk.Type != ctypes.PemBlkTypeCertificate {
// 		return nil, nil, fmt.Errorf("%w: invalid pem block type", ctypes.ErrInvalidCertificateValue)
// 	}
//
// 	cert, err := x509.ParseCertificate(blk.Bytes)
// 	if err != nil {
// 		return nil, nil, err
// 	}
//
// 	blk, rest = pem.Decode(certData.Certificate.Pubkey)
// 	if blk == nil || len(rest) > 0 {
// 		return nil, nil, ctypes.ErrInvalidPubkeyValue
// 	} else if blk.Type != ctypes.PemBlkTypeECPublicKey {
// 		return nil, nil, fmt.Errorf("%w: invalid pem block type", ctypes.ErrInvalidPubkeyValue)
// 	}
//
// 	pubkey, err := x509.ParsePKIXPublicKey(blk.Bytes)
// 	if err != nil {
// 		return nil, nil, err
// 	}
//
// 	return cert, pubkey, nil
// }
//
// func (c *client) NewJWT() (string, error) {
// 	claims := ajwt.Claims{
// 		RegisteredClaims: jwt.RegisteredClaims{
// 			Issuer:    c.signer.GetAddress().String(),
// 			IssuedAt:  jwt.NewNumericDate(time.Now()),
// 			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
// 		},
// 		Version: "v1",
// 		Leases:  ajwt.Leases{Access: ajwt.AccessTypeFull},
// 	}
//
// 	tok := jwt.NewWithClaims(ajwt.SigningMethodES256K, &claims)
//
// 	return tok.SignedString(c.signer)
// }
//
// func (c *client) newReqClient(ctx context.Context) *ReqClient {
// 	cl := &ReqClient{
// 		ctx:     ctx,
// 		host:    c.host,
// 		addr:    c.addr,
// 		cclient: c.cclient,
// 	}
//
// 	httpClient := &http.Client{
// 		Transport: &http.Transport{
// 			TLSClientConfig: c.tlsCfg,
// 		},
// 		// Never follow redirects
// 		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
// 			return http.ErrUseLastResponse
// 		},
// 		Jar:     nil,
// 		Timeout: 0,
// 	}
//
// 	cl.hclient = httpClient
//
// 	cl.wsclient = &websocket.Dialer{
// 		Proxy:            http.ProxyFromEnvironment,
// 		HandshakeTimeout: 45 * time.Second,
// 		TLSClientConfig:  c.tlsCfg,
// 	}
//
// 	return cl
// }
//
// type ClientResponseError struct {
// 	Status  int
// 	Message string
// }
//
// func (err ClientResponseError) Error() string {
// 	return fmt.Sprintf("remote server returned %d", err.Status)
// }
//
// func (err ClientResponseError) ClientError() string {
// 	return fmt.Sprintf("Remote Server returned %d\n%s", err.Status, err.Message)
// }
//
// func (c *client) Status(ctx context.Context) (*provider.Status, error) {
// 	uri, err := makeURI(c.host, statusPath())
// 	if err != nil {
// 		return nil, err
// 	}
// 	var obj provider.Status
//
// 	if err := c.getStatus(ctx, uri, &obj); err != nil {
// 		return nil, err
// 	}
//
// 	return &obj, nil
// }
//
// func (c *client) Validate(ctx context.Context, gspec dtypes.GroupSpec) (provider.ValidateGroupSpecResult, error) {
// 	uri, err := makeURI(c.host, validatePath())
// 	if err != nil {
// 		return provider.ValidateGroupSpecResult{}, err
// 	}
//
// 	if err = gspec.ValidateBasic(); err != nil {
// 		return provider.ValidateGroupSpecResult{}, err
// 	}
//
// 	bgspec, err := json.Marshal(gspec)
// 	if err != nil {
// 		return provider.ValidateGroupSpecResult{}, err
// 	}
//
// 	req, err := http.NewRequestWithContext(ctx, "GET", uri, bytes.NewReader(bgspec))
// 	if err != nil {
// 		return provider.ValidateGroupSpecResult{}, err
// 	}
// 	req.Header.Set("Content-Type", contentTypeJSON)
//
// 	if len(c.certs) == 0 && c.signer != nil {
// 		token, err := c.NewJWT()
// 		if err != nil {
// 			return provider.ValidateGroupSpecResult{}, err
// 		}
//
// 		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
// 	}
//
// 	rCl := c.newReqClient(ctx)
// 	resp, err := rCl.hclient.Do(req)
// 	if err != nil {
// 		return provider.ValidateGroupSpecResult{}, err
// 	}
//
// 	buf := &bytes.Buffer{}
// 	_, err = io.Copy(buf, resp.Body)
// 	defer func() {
// 		_ = resp.Body.Close()
// 	}()
//
// 	if err != nil {
// 		return provider.ValidateGroupSpecResult{}, err
// 	}
//
// 	err = createClientResponseErrorIfNotOK(resp, buf)
// 	if err != nil {
// 		return provider.ValidateGroupSpecResult{}, err
// 	}
//
// 	var obj provider.ValidateGroupSpecResult
// 	if err = json.NewDecoder(buf).Decode(&obj); err != nil {
// 		return provider.ValidateGroupSpecResult{}, err
// 	}
//
// 	return obj, nil
// }
//
// func (c *client) SubmitManifest(ctx context.Context, dseq uint64, mani manifest.Manifest) error {
// 	uri, err := makeURI(c.host, submitManifestPath(dseq))
// 	if err != nil {
// 		return err
// 	}
//
// 	buf, err := json.Marshal(mani)
// 	if err != nil {
// 		return err
// 	}
//
// 	req, err := http.NewRequestWithContext(ctx, "PUT", uri, bytes.NewBuffer(buf))
// 	if err != nil {
// 		return err
// 	}
//
// 	if len(c.certs) == 0 && c.signer != nil {
// 		token, err := c.NewJWT()
// 		if err != nil {
// 			return err
// 		}
//
// 		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
// 	}
//
// 	req.Header.Set("Content-Type", contentTypeJSON)
//
// 	rCl := c.newReqClient(ctx)
// 	resp, err := rCl.hclient.Do(req)
//
// 	if err != nil {
// 		return err
// 	}
// 	responseBuf := &bytes.Buffer{}
// 	_, err = io.Copy(responseBuf, resp.Body)
// 	defer func() {
// 		_ = resp.Body.Close()
// 	}()
//
// 	if err != nil {
// 		return err
// 	}
//
// 	return createClientResponseErrorIfNotOK(resp, responseBuf)
// }
//
// func (c *client) GetManifest(ctx context.Context, lid mtypes.LeaseID) (manifest.Manifest, error) {
// 	uri, err := makeURI(c.host, getManifestPath(lid))
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	req, err := http.NewRequestWithContext(ctx, "GET", uri, nil)
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	if len(c.certs) == 0 && c.signer != nil {
// 		token, err := c.NewJWT()
// 		if err != nil {
// 			return nil, err
// 		}
//
// 		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
// 	}
//
// 	rCl := c.newReqClient(ctx)
// 	resp, err := rCl.hclient.Do(req)
//
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	defer func() {
// 		_ = resp.Body.Close()
// 	}()
//
// 	body, err := io.ReadAll(resp.Body)
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	err = createClientResponseErrorIfNotOK(resp, bytes.NewBuffer(body))
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	var mani manifest.Manifest
// 	if err = json.Unmarshal(body, &mani); err != nil {
// 		return nil, err
// 	}
//
// 	return mani, nil
// }
//
// func (c *client) MigrateEndpoints(ctx context.Context, endpoints []string, dseq uint64, gseq uint32) error {
// 	uri, err := makeURI(c.host, "endpoint/migrate")
// 	if err != nil {
// 		return err
// 	}
//
// 	body := endpointMigrateRequestBody{
// 		EndpointsToMigrate: endpoints,
// 		DestinationDSeq:    dseq,
// 		DestinationGSeq:    gseq,
// 	}
//
// 	buf, err := json.Marshal(body)
// 	if err != nil {
// 		return err
// 	}
//
// 	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, bytes.NewReader(buf))
// 	if err != nil {
// 		return err
// 	}
//
// 	if len(c.certs) == 0 && c.signer != nil {
// 		token, err := c.NewJWT()
// 		if err != nil {
// 			return err
// 		}
//
// 		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
// 	}
//
// 	req.Header.Set("Content-Type", contentTypeJSON)
//
// 	rCl := c.newReqClient(ctx)
// 	resp, err := rCl.hclient.Do(req)
// 	if err != nil {
// 		return err
// 	}
// 	responseBuf := &bytes.Buffer{}
// 	_, err = io.Copy(responseBuf, resp.Body)
// 	defer func() {
// 		_ = resp.Body.Close()
// 	}()
//
// 	if err != nil {
// 		return err
// 	}
//
// 	return createClientResponseErrorIfNotOK(resp, responseBuf)
// }
//
// func (c *client) MigrateHostnames(ctx context.Context, hostnames []string, dseq uint64, gseq uint32) error {
// 	uri, err := makeURI(c.host, "hostname/migrate")
// 	if err != nil {
// 		return err
// 	}
//
// 	body := migrateRequestBody{
// 		HostnamesToMigrate: hostnames,
// 		DestinationDSeq:    dseq,
// 		DestinationGSeq:    gseq,
// 	}
//
// 	buf, err := json.Marshal(body)
// 	if err != nil {
// 		return err
// 	}
//
// 	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, bytes.NewReader(buf))
// 	if err != nil {
// 		return err
// 	}
//
// 	if len(c.certs) == 0 && c.signer != nil {
// 		token, err := c.NewJWT()
// 		if err != nil {
// 			return err
// 		}
//
// 		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
// 	}
//
// 	req.Header.Set("Content-Type", contentTypeJSON)
//
// 	rCl := c.newReqClient(ctx)
// 	resp, err := rCl.hclient.Do(req)
// 	if err != nil {
// 		return err
// 	}
// 	responseBuf := &bytes.Buffer{}
// 	_, err = io.Copy(responseBuf, resp.Body)
// 	defer func() {
// 		_ = resp.Body.Close()
// 	}()
//
// 	if err != nil {
// 		return err
// 	}
//
// 	return createClientResponseErrorIfNotOK(resp, responseBuf)
// }
//
// func (c *client) LeaseStatus(ctx context.Context, id mtypes.LeaseID) (LeaseStatus, error) {
// 	uri, err := makeURI(c.host, leaseStatusPath(id))
// 	if err != nil {
// 		return LeaseStatus{}, err
// 	}
//
// 	var obj LeaseStatus
// 	if err := c.getStatus(ctx, uri, &obj); err != nil {
// 		return LeaseStatus{}, err
// 	}
//
// 	return obj, nil
// }
//
// func (c *client) LeaseEvents(ctx context.Context, id mtypes.LeaseID, _ string, follow bool) (*LeaseKubeEvents, error) {
// 	endpoint, err := url.Parse(c.host.String() + "/" + leaseEventsPath(id))
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	switch endpoint.Scheme {
// 	case schemeWSS, schemeHTTPS:
// 		endpoint.Scheme = schemeWSS
// 	default:
// 		return nil, errors.Errorf("invalid uri scheme %q", endpoint.Scheme)
// 	}
//
// 	query := url.Values{}
// 	query.Set("follow", strconv.FormatBool(follow))
//
// 	endpoint.RawQuery = query.Encode()
// 	rCl := c.newReqClient(ctx)
//
// 	var hdr http.Header
//
// 	if len(c.certs) == 0 && c.signer != nil {
// 		token, err := c.NewJWT()
// 		if err != nil {
// 			return nil, err
// 		}
//
// 		hdr = make(http.Header)
// 		hdr.Set("Authorization", fmt.Sprintf("Bearer %s", token))
// 	}
//
// 	conn, response, err := rCl.wsclient.DialContext(ctx, endpoint.String(), hdr)
// 	if err != nil {
// 		if errors.Is(err, websocket.ErrBadHandshake) {
// 			buf := &bytes.Buffer{}
// 			_, _ = io.Copy(buf, response.Body)
//
// 			return nil, ClientResponseError{
// 				Status:  response.StatusCode,
// 				Message: buf.String(),
// 			}
// 		}
//
// 		return nil, err
// 	}
//
// 	streamch := make(chan cltypes.LeaseEvent)
// 	onclose := make(chan string, 1)
// 	logs := &LeaseKubeEvents{
// 		Stream:  streamch,
// 		OnClose: onclose,
// 	}
//
// 	processOnCloseErr := func(err error) {
// 		if err != nil {
// 			if _, ok := err.(*websocket.CloseError); ok { // nolint: gosimple
// 				onclose <- parseCloseMessage(err.Error())
// 			} else {
// 				onclose <- err.Error()
// 			}
// 		}
// 	}
//
// 	if err = conn.SetReadDeadline(time.Now().Add(pingWait)); err != nil {
// 		return nil, err
// 	}
//
// 	conn.SetPingHandler(func(string) error {
// 		err := conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(time.Second))
// 		if err != nil {
// 			return err
// 		}
//
// 		return conn.SetReadDeadline(time.Now().Add(pingWait))
// 	})
//
// 	go func(conn *websocket.Conn) {
// 		defer func() {
// 			close(streamch)
// 			close(onclose)
// 			_ = conn.Close()
// 		}()
//
// 		for {
// 			mType, msg, e := conn.ReadMessage()
// 			if e != nil {
// 				processOnCloseErr(e)
// 				return
// 			}
//
// 			switch mType {
// 			case websocket.TextMessage:
// 				var evt cltypes.LeaseEvent
// 				if e = json.Unmarshal(msg, &evt); e != nil {
// 					onclose <- e.Error()
// 					return
// 				}
//
// 				streamch <- evt
// 			case websocket.CloseMessage:
// 				onclose <- parseCloseMessage(string(msg))
// 				return
// 			default:
// 			}
// 		}
// 	}(conn)
//
// 	return logs, nil
// }
//
// func (c *client) ServiceStatus(ctx context.Context, id mtypes.LeaseID, service string) (*cltypes.ServiceStatus, error) {
// 	uri, err := makeURI(c.host, serviceStatusPath(id, service))
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	var obj cltypes.ServiceStatus
// 	if err := c.getStatus(ctx, uri, &obj); err != nil {
// 		return nil, err
// 	}
//
// 	return &obj, nil
// }
//
// func (c *client) getStatus(ctx context.Context, uri string, obj interface{}) error {
// 	req, err := http.NewRequestWithContext(ctx, "GET", uri, nil)
// 	if err != nil {
// 		return err
// 	}
//
// 	if len(c.certs) == 0 && c.signer != nil {
// 		token, err := c.NewJWT()
// 		if err != nil {
// 			return err
// 		}
//
// 		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
// 	}
//
// 	req.Header.Set("Content-Type", contentTypeJSON)
//
// 	rCl := c.newReqClient(ctx)
// 	resp, err := rCl.hclient.Do(req)
// 	if err != nil {
// 		return err
// 	}
//
// 	buf := &bytes.Buffer{}
// 	_, err = io.Copy(buf, resp.Body)
// 	defer func() {
// 		_ = resp.Body.Close()
// 	}()
//
// 	if err != nil {
// 		return err
// 	}
//
// 	err = createClientResponseErrorIfNotOK(resp, buf)
// 	if err != nil {
// 		return err
// 	}
//
// 	dec := json.NewDecoder(buf)
// 	return dec.Decode(obj)
// }
//
// func createClientResponseErrorIfNotOK(resp *http.Response, responseBuf *bytes.Buffer) error {
// 	if resp.StatusCode == http.StatusOK {
// 		return nil
// 	}
//
// 	return ClientResponseError{
// 		Status:  resp.StatusCode,
// 		Message: responseBuf.String(),
// 	}
// }
//
// // makeURI
// // for client queries path must not include owner id
// func makeURI(uri *url.URL, path string) (string, error) {
// 	endpoint, err := url.Parse(uri.String() + "/" + path)
// 	if err != nil {
// 		return "", err
// 	}
//
// 	return endpoint.String(), nil
// }
//
// func (c *client) LeaseLogs(ctx context.Context,
// 	id mtypes.LeaseID,
// 	services string,
// 	follow bool,
// 	_ int64) (*ServiceLogs, error) {
//
// 	endpoint, err := url.Parse(c.host.String() + "/" + serviceLogsPath(id))
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	switch endpoint.Scheme {
// 	case schemeWSS, schemeHTTPS:
// 		endpoint.Scheme = schemeWSS
// 	default:
// 		return nil, errors.Errorf("invalid uri scheme \"%s\"", endpoint.Scheme)
// 	}
//
// 	query := url.Values{}
//
// 	query.Set("follow", strconv.FormatBool(follow))
//
// 	if services != "" {
// 		query.Set("services", services)
// 	}
//
// 	endpoint.RawQuery = query.Encode()
//
// 	rCl := c.newReqClient(ctx)
//
// 	var hdr http.Header
//
// 	if len(c.certs) == 0 && c.signer != nil {
// 		token, err := c.NewJWT()
// 		if err != nil {
// 			return nil, err
// 		}
//
// 		hdr = make(http.Header)
// 		hdr.Set("Authorization", fmt.Sprintf("Bearer %s", token))
// 	}
//
// 	conn, response, err := rCl.wsclient.DialContext(ctx, endpoint.String(), hdr)
// 	if err != nil {
// 		if errors.Is(err, websocket.ErrBadHandshake) {
// 			buf := &bytes.Buffer{}
// 			_, _ = io.Copy(buf, response.Body)
//
// 			return nil, ClientResponseError{
// 				Status:  response.StatusCode,
// 				Message: buf.String(),
// 			}
// 		}
//
// 		return nil, err
// 	}
//
// 	streamch := make(chan ServiceLogMessage)
// 	onclose := make(chan string, 1)
// 	logs := &ServiceLogs{
// 		Stream:  streamch,
// 		OnClose: onclose,
// 	}
//
// 	if err = conn.SetReadDeadline(time.Now().Add(pingWait)); err != nil {
// 		return nil, err
// 	}
//
// 	conn.SetPingHandler(func(string) error {
// 		err := conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(time.Second))
// 		if err != nil {
// 			return err
// 		}
//
// 		return conn.SetReadDeadline(time.Now().Add(pingWait))
// 	})
//
// 	go func(conn *websocket.Conn) {
// 		defer func() {
// 			close(streamch)
// 			close(onclose)
// 			_ = conn.Close()
// 		}()
//
// 		for {
// 			mType, msg, e := conn.ReadMessage()
// 			if e != nil {
// 				onclose <- parseCloseMessage(e.Error())
// 				return
// 			}
//
// 			switch mType {
// 			case websocket.TextMessage:
// 				var logLine ServiceLogMessage
// 				if e = json.Unmarshal(msg, &logLine); e != nil {
// 					return
// 				}
//
// 				streamch <- logLine
// 			case websocket.CloseMessage:
// 				onclose <- parseCloseMessage(string(msg))
// 				return
// 			default:
// 			}
// 		}
// 	}(conn)
//
// 	return logs, nil
// }
//
// // parseCloseMessage extract close reason from websocket close message
// // "websocket: [error code]: [client reason]"
// func parseCloseMessage(msg string) string {
// 	errmsg := strings.SplitN(msg, ": ", 3)
// 	if len(errmsg) == 3 {
// 		return errmsg[2]
// 	}
//
// 	return ""
// }
