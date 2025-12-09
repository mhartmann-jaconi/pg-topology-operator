/*
Package webhook handles the creation of Kubernetes webhook configurations.

All webhooks should have a BuildWebhook function that returns a [admissionregistrationv1.MutatingWebhookConfiguration]
for that webhook. The function takes the CA certificate as a parameter, which is used to sign the TLS certificates for
the webhook server.

Example:

	func BuildWebhook(ca *bytes.Buffer) *admissionregistrationv1.MutatingWebhookConfiguration {
		return webhook.Build(ca, "/mutate--v1-pods", "my-pod-mutator", "", "v1", "pods")
	}

The BuildWebhook function can then be passed to [Create] to create the webhook configurations in the Kubernetes cluster.

Example:

	if err := webhook.Create(ctx, ca, myfirstwebhook.BuildWebhook, mysecondwebhook.BuildWebhook); err != nil {
		panic(err)
	}
*/
package webhook

import (
	"bytes"
	"context"
	"fmt"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"

	"github.com/spf13/viper"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const namePattern = "pg-%s.jaconi.io" // replaced with K8s resource name

// Build a [admissionregistrationv1.MutatingWebhookConfiguration] for the given parameters. This function should be used
// as a helper function when building your webhooks BuildWebhook function as described above.
func Build(ca *bytes.Buffer, endpoint, configName, apiGroup, apiVersion, apiResource string) *admissionregistrationv1.MutatingWebhookConfiguration {
	operatorNamespace := viper.GetString(config.ServiceNamespace)
	operatorServiceName := viper.GetString(config.ServiceName)
	port := viper.GetInt32(config.ServicePort)

	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: configName,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{{
			Name: fmt.Sprintf(namePattern, apiResource),
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: viper.GetStringMapString(config.PgSelector),
			},
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				CABundle: ca.Bytes(), // CA bundle created earlier.
				Service: &admissionregistrationv1.ServiceReference{
					Name:      operatorServiceName,
					Namespace: operatorNamespace,
					Path:      &endpoint,
					Port:      &port,
				},
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{
					admissionregistrationv1.Create,
				},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{apiGroup},
					APIVersions: []string{apiVersion},
					Resources:   []string{apiResource},
				},
			}},
			AdmissionReviewVersions: []string{"v1"},
			FailurePolicy:           ptr.To(admissionregistrationv1.Fail),
			TimeoutSeconds:          ptr.To(int32(2)),
			SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
		}},
	}
}

// Create a [admissionregistrationv1.MutatingWebhookConfiguration] for each webhook by injecting the CA certificate.
func Create(ctx context.Context, client kubernetes.Interface, ca *bytes.Buffer, builders ...func(*bytes.Buffer) *admissionregistrationv1.MutatingWebhookConfiguration) error {
	for _, builder := range builders {
		webhook := builder(ca)
		err := createOrUpdate(ctx, client, webhook)
		if err != nil {
			return err
		}
	}

	return nil
}

func createOrUpdate(ctx context.Context, client kubernetes.Interface, webhook *admissionregistrationv1.MutatingWebhookConfiguration) error {
	existing, err := client.AdmissionregistrationV1().
		MutatingWebhookConfigurations().
		Get(ctx, webhook.Name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		// If the configuration does not exist yet, create it.
		_, err := client.AdmissionregistrationV1().
			MutatingWebhookConfigurations().
			Create(ctx, webhook, metav1.CreateOptions{})
		return err
	} else {
		// Update the existing configuration.
		existing.Webhooks = webhook.Webhooks
		_, err := client.AdmissionregistrationV1().
			MutatingWebhookConfigurations().
			Update(context.TODO(), existing, metav1.UpdateOptions{})
		return err
	}
}
