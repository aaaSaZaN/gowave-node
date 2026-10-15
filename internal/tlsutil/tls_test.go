package tlsutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"github.com/remnawave/node-go/internal/config"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSNIEnforcedWithClientCertificate(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, _ = x509.ParseCertificate(rootDER)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: root.NotBefore, NotAfter: root.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cfg := &config.Config{SniVerification: true, Payload: &config.NodePayload{CACertPem: string(caPEM), NodeCertPem: string(certPEM), NodeKeyPem: string(keyPEM), JWTPublicKey: "test"}}
	serverConfig, err := SetupServerTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = serverConfig
	server.StartTLS()
	defer server.Close()
	clientCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	expected := config.DeriveSni(string(caPEM), "test")
	for _, name := range []string{"", "wrong.example", expected} {
		transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: name, Certificates: []tls.Certificate{clientCert}, MinVersion: tls.VersionTLS13}} // test CA, verify server-side mTLS/SNI
		client := &http.Client{Transport: transport, Timeout: time.Second}
		res, err := client.Get(server.URL)
		transport.CloseIdleConnections()
		if name == expected {
			if err != nil || res.StatusCode != 204 {
				t.Fatalf("valid SNI: %v", err)
			}
		} else if err == nil {
			res.Body.Close()
			t.Fatalf("accepted SNI %q", name)
		}
		if res != nil {
			res.Body.Close()
		}
	}
}
