package browser

import (
	"fmt"
	"testing"
	"time"

	"github.com/zarazaex69/mo/internal/pkg/tempmail"
)

func TestFullRegistration(t *testing.T) {
	// skip in CI
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}

	// create temp email
	mail := tempmail.New()
	email, err := mail.CreateEmail()
	if err != nil {
		t.Fatalf("create email: %v", err)
	}
	fmt.Printf("created email: %s\n", email.Address)

	// generate password
	password := "TestPass123!@#"
	name := "Test User"

	// launch browser (visible for captcha)
	browser, err := New(false)
	if err != nil {
		t.Fatalf("launch browser: %v", err)
	}
	defer browser.Close()

	// start registration
	creds := Credentials{
		Email:    email.Address,
		Password: password,
		Name:     name,
	}

	_, err = browser.RegisterZAI(creds)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	fmt.Println("waiting for verification email...")

	// wait for verification email
	msg, err := mail.WaitForMessage(email.Address, "z.ai", "Verify", 2*time.Minute, 3*time.Second)
	if err != nil {
		t.Fatalf("wait for email: %v", err)
	}
	if msg == nil {
		t.Fatal("no verification email received")
	}

	fmt.Printf("got email: %s\n", msg.Subject)

	// extract verify link
	link := tempmail.ExtractVerifyLink(msg.BodyText)
	if link == "" {
		t.Fatal("no verify link found")
	}
	fmt.Printf("verify link: %s\n", link)

	// complete verification
	token, err := browser.VerifyEmail(link, password)
	if err != nil {
		t.Fatalf("verify email: %v", err)
	}

	fmt.Printf("✓ got token: %s...\n", token[:50])
}
