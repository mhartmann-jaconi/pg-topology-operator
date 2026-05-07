package tls

import (
	"bytes"
	"os"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/spf13/viper"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
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
