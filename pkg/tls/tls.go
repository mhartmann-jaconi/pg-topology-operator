package tls

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/spf13/viper"
)

const CertDir = "/etc/webhook/certs/"
const CertFile = CertDir + "tls.crt"
const KeyFile = CertDir + "tls.key"
const caFile = CertDir + "ca.pem"

// Generate creates a certificate and key for TLS communication and stores them in [CertDir].
// This certificate will be signed by a CA, which is retrieved afterwards.
//
// Inspired by https://www.velotio.com/engineering-blog/managing-tls-certificate-for-kubernetes-admission-webhook
func Generate() (*bytes.Buffer, error) {

	if _, err := os.Stat(caFile); err == nil {
		// certificates already generated
		ca, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		return bytes.NewBuffer(ca), nil
	}

	organization := viper.GetString(config.CertificateOrganization)

	slog.Info("Generating certificates")
	var caPEM, serverCertPEM, serverPrivKeyPEM *bytes.Buffer
	// CA config
	ca := &x509.Certificate{
		Subject: pkix.Name{
			Organization: []string{organization},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	// CA private key
	slog.Debug("Generating CA private key")
	caPrivKey, err := rsa.GenerateKey(cryptorand.Reader, 4096)
	if err != nil {
		fmt.Println(err)
	}

	// Self signed CA certificate
	slog.Debug("Generating self-signed certificate")
	caBytes, err := x509.CreateCertificate(cryptorand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		fmt.Println(err)
	}

	// PEM encode CA cert
	slog.Debug("Encoding CA cert")
	caPEM = new(bytes.Buffer)
	_ = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	dnsNames := dnsNames()
	commonName := viper.GetString(config.ServiceName) + "." + viper.GetString(config.ServiceNamespace) + ".svc"

	// server cert config
	cert := &x509.Certificate{
		DNSNames: dnsNames,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{organization},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(1, 0, 0),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature,
	}

	// server private key
	slog.Debug("Generating server private key")
	serverPrivKey, err := rsa.GenerateKey(cryptorand.Reader, 4096)
	if err != nil {
		fmt.Println(err)
	}

	// sign the server cert
	slog.Debug("Signing server cert")
	serverCertBytes, err := x509.CreateCertificate(cryptorand.Reader, cert, ca, &serverPrivKey.PublicKey, caPrivKey)
	if err != nil {
		fmt.Println(err)
	}

	// PEM encode the  server cert and key
	slog.Debug("Encoding server cert")
	serverCertPEM = new(bytes.Buffer)
	_ = pem.Encode(serverCertPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCertBytes,
	})

	slog.Debug("Encoding server key")
	serverPrivKeyPEM = new(bytes.Buffer)
	_ = pem.Encode(serverPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverPrivKey),
	})

	slog.Info(fmt.Sprintf("Writing certificates to directory %s", CertDir))

	err = writeFile(CertFile, serverCertPEM)
	if err != nil {
		slog.Error(err.Error())
		return nil, err
	}

	err = writeFile(KeyFile, serverPrivKeyPEM)
	if err != nil {
		slog.Error(err.Error())
		return nil, err
	}

	err = writeFile(caFile, caPEM)
	if err != nil {
		slog.Error(err.Error())
		return nil, err
	}

	return caPEM, nil
}

func dnsNames() []string {
	var arr = make([]string, 3)
	var svcName = viper.GetString(config.ServiceName)
	var svcNs = viper.GetString(config.ServiceNamespace)
	arr[0] = svcName
	arr[1] = svcName + "." + svcNs
	arr[2] = svcName + "." + svcNs + ".svc"
	return arr
}

func writeFile(filepath string, sCert *bytes.Buffer) error {
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(sCert.Bytes())
	if err != nil {
		return err
	}
	return nil
}
