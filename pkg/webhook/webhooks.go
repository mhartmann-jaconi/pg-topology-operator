/*
Package webhook handles the TLS cert update of Kubernetes webhook configurations.
*/
package webhook

import (
	"bytes"
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Update a [admissionregistrationv1.MutatingWebhookConfiguration] for each webhook by injecting the CA certificate.
func Update(ctx context.Context, client kubernetes.Interface, ca *bytes.Buffer, names ...string) error {
	for _, name := range names {
		err := update(ctx, client, ca, name)
		if err != nil {
			return err
		}
	}

	return nil
}

func update(ctx context.Context, client kubernetes.Interface, ca *bytes.Buffer, name string) error {
	existing, err := client.AdmissionregistrationV1().
		MutatingWebhookConfigurations().
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	} else {
		// Update the existing configuration.
		existing.Webhooks[0].ClientConfig.CABundle = ca.Bytes()
		_, err := client.AdmissionregistrationV1().
			MutatingWebhookConfigurations().
			Update(context.TODO(), existing, metav1.UpdateOptions{})
		return err
	}
}
