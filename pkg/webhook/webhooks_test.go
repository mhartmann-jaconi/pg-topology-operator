package webhook_test

import (
	"bytes"
	"context"
	"fmt"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/webhook"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func ExampleBuild() {
	config := webhook.Build(bytes.NewBuffer([]byte("my-ca")), "/mutate--v1-pods", "my-pod-mutator", "", "v1", "pods")

	fmt.Println(string(config.Webhooks[0].ClientConfig.CABundle))
	fmt.Println(config.Name)
	fmt.Println(*config.Webhooks[0].ClientConfig.Service.Path)
	fmt.Println(config.Webhooks[0].Rules[0].APIGroups[0])
	fmt.Println(config.Webhooks[0].Rules[0].APIVersions[0])
	fmt.Println(config.Webhooks[0].Rules[0].Resources[0])
	// Output:
	// my-ca
	// my-pod-mutator
	// /mutate--v1-pods
	//
	// v1
	// pods
}

func ExampleCreate() {
	ca := bytes.NewBuffer([]byte("my-ca"))

	c := fake.NewClientset()

	_ = webhook.Create(context.Background(), c, ca, func(ca *bytes.Buffer) *admissionregistrationv1.MutatingWebhookConfiguration {
		return webhook.Build(ca, "/mutate--v1-pods", "my-pod-mutator", "", "v1", "pods")
	})

	config, _ := c.AdmissionregistrationV1().
		MutatingWebhookConfigurations().
		Get(context.Background(), "my-pod-mutator", metav1.GetOptions{})

	fmt.Println(string(config.Webhooks[0].ClientConfig.CABundle))
	fmt.Println(config.Name)
	fmt.Println(*config.Webhooks[0].ClientConfig.Service.Path)
	fmt.Println(config.Webhooks[0].Rules[0].APIGroups[0])
	fmt.Println(config.Webhooks[0].Rules[0].APIVersions[0])
	fmt.Println(config.Webhooks[0].Rules[0].Resources[0])
	// Output:
	// my-ca
	// my-pod-mutator
	// /mutate--v1-pods
	//
	// v1
	// pods
}
