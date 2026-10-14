package tls

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/spf13/viper"

	"vcs01.rsint.net/comvidence/sources/platform/tools/pg-topology-operator/pkg/config"
)

// TestGenerate_PropagateCryptoErrors test propagating errors
func TestGenerate_PropagateCryptoErrors(t *testing.T) {
	g := NewWithT(t)

	// non-existing cert directory
	viper.Set(config.CertificateDirectory, "/foo/bar")
	defer viper.Reset()

	_, err := Generate()
	g.Expect(err).NotTo(BeNil())
	g.Expect(err.Error()).To(ContainSubstring("no such file or directory"))
}

// TestGenerate_ReturnExistingCA verifies that Generate() returns an existing
// CA and does not overwrite that.
func TestGenerate_ReturnExistingCA(t *testing.T) {
	g := NewWithT(t)

	// Generate() writes to /etc/webhook/certs/ by default, which is not writable
	// in a test environment. We need a temp directory.
	tmpDir := t.TempDir() + "/"
	viper.Set(config.CertificateDirectory, tmpDir)
	defer viper.Reset()

	expectedCA := []byte("-----BEGIN CERTIFICATE-----\ntest-ca-content\n-----END CERTIFICATE-----\n")
	err := os.WriteFile(tmpDir+caFile, expectedCA, 0600)
	g.Expect(err).ToNot(HaveOccurred())

	result, err := Generate()
	g.Expect(err).To(BeNil())
	g.Expect(result.String()).To(Equal(string(expectedCA)))
}

func TestWriteFile_CreateFileWithAppropriatePermissions(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()
	testFile := tmpDir + "/test-key.pem"
	content := bytes.NewBufferString("-----BEGIN RSA PRIVATE KEY-----\ntest-key\n-----END RSA PRIVATE KEY-----\n")

	err := writeFile(testFile, content)
	g.Expect(err).ToNot(HaveOccurred())

	info, err := os.Stat(testFile)
	g.Expect(err).ToNot(HaveOccurred())

	// Private key files must be readable only by the owner (0600)
	g.Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)),
		"Private key files should have mode 0600, got %o", info.Mode().Perm())
}

// TestGenerate_CertificateHasSerialNumber verifies that generated CA
// certificates have a positive, non-zero serial number as required by
// RFC 5280 Section 4.1.2.2.
func TestGenerate_CertificateHasSerialNumber(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir() + "/"
	viper.Set(config.CertificateDirectory, tmpDir)
	viper.Set(config.CertificateOrganization, "test-org")
	viper.Set(config.ServiceName, "test-svc")
	viper.Set(config.ServiceNamespace, "test-ns")
	defer viper.Reset()

	caPEM, err := Generate()
	g.Expect(err).ToNot(HaveOccurred())

	// Parse the CA certificate
	block, _ := pem.Decode(caPEM.Bytes())
	g.Expect(block).ToNot(BeNil(), "CA PEM should be decodable")

	cert, err := x509.ParseCertificate(block.Bytes)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(cert.SerialNumber).ToNot(BeNil(), "Certificate serial number must not be nil")
	g.Expect(cert.SerialNumber.Sign()).To(Equal(1),
		"Certificate serial number must be a positive integer per RFC 5280")
}

// TestWriteFile_TruncatesExistingContent verifies that writeFile()
// properly truncates an existing file when writing shorter content.
func TestWriteFile_TruncatesExistingContent(t *testing.T) {
	g := NewWithT(t)

	tmpDir := t.TempDir()
	testFile := tmpDir + "/test-cert.pem"

	// Write a long initial content
	longContent := bytes.NewBufferString("-----BEGIN CERTIFICATE-----\nAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n-----END CERTIFICATE-----\n")
	err := writeFile(testFile, longContent)
	g.Expect(err).ToNot(HaveOccurred())

	// Overwrite with shorter content
	shortContent := bytes.NewBufferString("-----BEGIN CERTIFICATE-----\nBBBB\n-----END CERTIFICATE-----\n")
	err = writeFile(testFile, shortContent)
	g.Expect(err).ToNot(HaveOccurred())

	// Read back and verify no trailing bytes from the old content
	actual, err := os.ReadFile(testFile)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(string(actual)).To(Equal(shortContent.String()),
		"File should contain only the new content, with no trailing bytes from the previous write")
}
