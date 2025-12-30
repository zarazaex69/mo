package tempmail

import (
	"fmt"
	"testing"
)

func TestCreateAndFetch(t *testing.T) {
	client := New()

	// create new email
	email, err := client.CreateEmail()
	if err != nil {
		t.Fatalf("create email failed: %v", err)
	}

	fmt.Printf("created email: %s\n", email.Address)
	fmt.Printf("token: %s\n", email.Token)

	// fetch messages (should be empty)
	messages, err := client.GetMessages(email.Address)
	if err != nil {
		t.Fatalf("get messages failed: %v", err)
	}

	fmt.Printf("messages: %d\n", len(messages))
	for _, m := range messages {
		fmt.Printf("  [%s] %s - %s\n", m.ID, m.From, m.Subject)
	}
}

func TestExtractVerifyLink(t *testing.T) {
	text := `Hello test,

Thank you for registering with Z.ai! Please copy the link below:

https://chat.z.ai/auth/verify_email?token=abc-123&amp;email=test@test.com&amp;username=test&amp;language=en

This link will expire in 24 hours.`

	link := ExtractVerifyLink(text)
	expected := "https://chat.z.ai/auth/verify_email?token=abc-123&email=test@test.com&username=test&language=en"

	if link != expected {
		t.Errorf("expected %s, got %s", expected, link)
	}

	fmt.Printf("extracted link: %s\n", link)
}
