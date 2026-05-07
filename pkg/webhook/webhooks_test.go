package webhook

import (
	"bytes"
	"context"
	"testing"

	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestUpdate_PanicsOnEmptyWebhookList demonstrates that the update() function
// panics as the webhook config in the cluster is invalid and the operator is
// therefore unusable.
func TestUpdate_PanicsOnEmptyWebhookList(t *testing.T) {

	ctx := context.Background()

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expect panic for invalid webhook configuration")
		}
	}()

	// Create a MutatingWebhookConfiguration with zero webhooks
	emptyConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "empty-webhook",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{}, // no webhooks
	}

	client := fake.NewClientset(emptyConfig)
	ca := bytes.NewBufferString("fake-ca-bundle")

	// Expect panic
	Update(ctx, client, ca, "empty-webhook")
}

// TestUpdate_OnlyInjectsCAIntoFirstWebhook tests that all webhooks in
// a configuration get the same CA certificate.
func TestUpdate_InjectCAToAllWebhooks(t *testing.T) {

	g := NewWithT(t)
	ctx := context.Background()

	sidecarEffect := admissionregistrationv1.SideEffectClassNone

	// Create a MutatingWebhookConfiguration with two webhooks
	multiConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "multi-webhook",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name:                    "first.webhook.io",
				SideEffects:             &sidecarEffect,
				AdmissionReviewVersions: []string{"v1"},
				ClientConfig:            admissionregistrationv1.WebhookClientConfig{},
			},
			{
				Name:                    "second.webhook.io",
				SideEffects:             &sidecarEffect,
				AdmissionReviewVersions: []string{"v1"},
				ClientConfig:            admissionregistrationv1.WebhookClientConfig{},
			},
		},
	}

	client := fake.NewClientset(multiConfig)
	ca := bytes.NewBufferString("fake-ca-bundle")

	err := Update(ctx, client, ca, "multi-webhook")
	g.Expect(err).ToNot(HaveOccurred())

	// Verify the CA was injected into ALL webhooks, not just the first one
	updated, err := client.AdmissionregistrationV1().
		MutatingWebhookConfigurations().
		Get(ctx, "multi-webhook", metav1.GetOptions{})
	g.Expect(err).ToNot(HaveOccurred())

	for i, wh := range updated.Webhooks {
		g.Expect(wh.ClientConfig.CABundle).ToNot(BeEmpty(),
			"Webhook %d (%s) must have CA bundle injected", i, wh.Name)
	}
}
