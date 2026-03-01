package aarv

import "crypto/x509"

// newCertPool returns a new x509.CertPool.
// Separated to keep app.go independent of crypto/x509 import weight.
func newCertPool() *x509.CertPool {
	return x509.NewCertPool()
}
