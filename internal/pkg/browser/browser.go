package browser

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

//go:embed waiting.html
var waitingHTML string

type Browser struct {
	browser *rod.Browser
	page    *rod.Page
}

type Credentials struct {
	Email    string
	Password string
	Name     string
}

func New(headless bool) (*Browser, error) {
	// launch with stealth flags to avoid detection
	url := launcher.New().
		Headless(headless).
		Set("disable-blink-features", "AutomationControlled").
		MustLaunch()

	browser := rod.New().ControlURL(url).MustConnect()

	return &Browser{browser: browser}, nil
}

func (b *Browser) Close() {
	if b.browser != nil {
		b.browser.MustClose()
	}
}

func (b *Browser) RegisterZAI(creds Credentials) (string, error) {
	// use stealth page to avoid bot detection
	page, err := stealth.Page(b.browser)
	if err != nil {
		return "", fmt.Errorf("create stealth page: %w", err)
	}
	b.page = page

	page.MustSetViewport(1853, 943, 1, false)

	// open z.ai first
	page.MustNavigate("https://chat.z.ai/auth")

	// show instructions in second tab
	infoPage := b.browser.MustPage(b.waitingPageHTML())
	time.Sleep(4 * time.Second)
	infoPage.MustClose()

	// switch back to main page
	page.MustActivate()

	// wait for splash screen to disappear and page to load
	page.MustWaitLoad()
	time.Sleep(2 * time.Second)

	// click "Continue with Email" button
	emailBtn, err := page.Timeout(30*time.Second).ElementR("button", "[Ee]mail")
	if err != nil {
		return "", fmt.Errorf("find email button: %w", err)
	}
	if err := emailBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return "", fmt.Errorf("click email button: %w", err)
	}

	page.MustWaitStable()

	// click "Sign up" link to switch to registration form
	signUpLink, err := page.Timeout(10*time.Second).ElementR("button", "Sign up")
	if err != nil {
		return "", fmt.Errorf("find sign up link: %w", err)
	}
	if err := signUpLink.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return "", fmt.Errorf("click sign up: %w", err)
	}

	page.MustWaitStable()

	// fill registration form
	if err := b.fill(`[placeholder="Enter Your Full Name"]`, creds.Name); err != nil {
		return "", fmt.Errorf("fill name: %w", err)
	}

	if err := b.fill(`[name="email"]`, creds.Email); err != nil {
		return "", fmt.Errorf("fill email: %w", err)
	}

	if err := b.fill(`[placeholder="Enter Your Password"]`, creds.Password); err != nil {
		return "", fmt.Errorf("fill password: %w", err)
	}

	// user needs to solve captcha manually
	log.Println("waiting for captcha to be solved...")

	if err := b.waitForCaptcha(); err != nil {
		return "", fmt.Errorf("captcha timeout: %w", err)
	}

	log.Println("captcha solved")

	// click Create Account
	if err := b.click(`.ButtonSignIn`); err != nil {
		return "", fmt.Errorf("click create account: %w", err)
	}

	return "", nil
}

func (b *Browser) VerifyEmail(verifyURL, password string) (string, error) {
	// use stealth page
	page, err := stealth.Page(b.browser)
	if err != nil {
		return "", fmt.Errorf("create stealth page: %w", err)
	}
	b.page = page

	page.MustNavigate(verifyURL)
	page.MustSetViewport(1853, 943, 1, false)
	page.MustWaitStable()

	// fill password fields
	if err := b.fill(`#password`, password); err != nil {
		return "", fmt.Errorf("fill password: %w", err)
	}

	if err := b.fill(`#confirmPassword`, password); err != nil {
		return "", fmt.Errorf("fill confirm password: %w", err)
	}

	// click Complete Registration
	if err := b.click(`.buttonGradient`); err != nil {
		return "", fmt.Errorf("click complete: %w", err)
	}

	// wait for redirect to chat.z.ai
	log.Println("waiting for redirect to chat.z.ai...")
	if err := b.waitForRedirect("https://chat.z.ai"); err != nil {
		return "", fmt.Errorf("redirect timeout: %w", err)
	}

	log.Println("redirected, extracting token...")

	// wait for token in cookies
	token, err := b.waitForToken()
	if err != nil {
		return "", fmt.Errorf("get token: %w", err)
	}

	return token, nil
}

func (b *Browser) click(selector string) error {
	el, err := b.page.Timeout(10 * time.Second).Element(selector)
	if err != nil {
		return err
	}
	return el.Click(proto.InputMouseButtonLeft, 1)
}

func (b *Browser) clickText(text string) error {
	el, err := b.page.Timeout(10*time.Second).ElementR("*", text)
	if err != nil {
		return err
	}
	return el.Click(proto.InputMouseButtonLeft, 1)
}

func (b *Browser) fill(selector, value string) error {
	el, err := b.page.Timeout(10 * time.Second).Element(selector)
	if err != nil {
		return err
	}
	return el.Input(value)
}

func (b *Browser) waitForCaptcha() error {
	// wait up to 2 minutes for user to solve captcha
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("captcha timeout after 2 minutes")
		case <-ticker.C:
			// check for "Verification Passed" text
			el, err := b.page.Timeout(100*time.Millisecond).ElementR("span", "Verification Passed")
			if err == nil && el != nil {
				return nil
			}
		}
	}
}

func (b *Browser) waitForRedirect(urlPrefix string) error {
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("redirect timeout")
		case <-ticker.C:
			info := b.page.MustInfo()
			if strings.HasPrefix(info.URL, urlPrefix) {
				// wait for page to fully load
				time.Sleep(2 * time.Second)
				return nil
			}
		}
	}
}

func (b *Browser) waitForToken() (string, error) {
	// wait for /api/models request and extract token from cookies
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return "", fmt.Errorf("token timeout")
		case <-ticker.C:
			cookies, err := b.page.Cookies([]string{"https://chat.z.ai"})
			if err != nil {
				continue
			}

			for _, c := range cookies {
				if c.Name == "token" {
					return c.Value, nil
				}
			}
		}
	}
}

func (b *Browser) waitingPageHTML() string {
	encoded := base64.StdEncoding.EncodeToString([]byte(waitingHTML))
	return "data:text/html;base64," + encoded
}

func (b *Browser) RegisterQwen(email, password, name string) error {
	page, err := stealth.Page(b.browser)
	if err != nil {
		return fmt.Errorf("create stealth page: %w", err)
	}
	b.page = page

	page.MustSetViewport(2069, 1053, 1, false)
	page.MustNavigate("https://chat.qwen.ai/auth")
	page.MustWaitLoad()
	time.Sleep(2 * time.Second)

	signUpBtn, err := page.Timeout(10 * time.Second).Element(".qwenchat-auth-pc-switch-button")
	if err != nil {
		return fmt.Errorf("find sign up button: %w", err)
	}
	signUpBtn.MustClick()
	time.Sleep(1 * time.Second)

	if err := b.fill(`[placeholder="Enter Your Full Name"]`, name); err != nil {
		return fmt.Errorf("fill name: %w", err)
	}

	if err := b.fill(`[placeholder="Enter Your Email"]`, email); err != nil {
		return fmt.Errorf("fill email: %w", err)
	}

	if err := b.fill(`[placeholder="Enter Your Password"]`, password); err != nil {
		return fmt.Errorf("fill password: %w", err)
	}

	if err := b.fill(`[placeholder="Enter Your Password Again"]`, password); err != nil {
		return fmt.Errorf("fill confirm password: %w", err)
	}

	checkbox, err := page.Timeout(5 * time.Second).Element(".ant-checkbox-input")
	if err == nil {
		checkbox.MustClick()
	}

	log.Println("waiting for captcha to be solved...")

	infoPage := b.browser.MustPage(b.waitingPageHTML())
	time.Sleep(2 * time.Second)
	infoPage.MustClose()
	page.MustActivate()

	if err := b.waitForQwenCaptcha(); err != nil {
		return fmt.Errorf("captcha timeout: %w", err)
	}

	log.Println("captcha solved, clicking create account...")

	submitBtn, err := page.Timeout(10 * time.Second).Element(".qwenchat-auth-pc-submit-button")
	if err != nil {
		return fmt.Errorf("find submit button: %w", err)
	}
	submitBtn.MustClick()

	time.Sleep(3 * time.Second)
	return nil
}

func (b *Browser) waitForQwenCaptcha() error {
	timeout := time.After(3 * time.Minute)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("captcha timeout after 3 minutes")
		case <-ticker.C:
			el, err := b.page.Timeout(100 * time.Millisecond).Element(".qwenchat-auth-pc-submit-button")
			if err != nil {
				continue
			}
			disabled, _ := el.Attribute("disabled")
			if disabled == nil {
				return nil
			}
		}
	}
}

func (b *Browser) ActivateQwen(activationURL string) error {
	if b.page == nil {
		page, err := stealth.Page(b.browser)
		if err != nil {
			return fmt.Errorf("create stealth page: %w", err)
		}
		b.page = page
		b.page.MustSetViewport(2069, 1053, 1, false)
	}

	b.page.MustNavigate(activationURL)
	b.page.MustWaitLoad()
	time.Sleep(3 * time.Second)

	return nil
}

func (b *Browser) ConfirmQwenAuth(verificationURL string) error {
	if b.page == nil {
		page, err := stealth.Page(b.browser)
		if err != nil {
			return fmt.Errorf("create stealth page: %w", err)
		}
		b.page = page
		b.page.MustSetViewport(2069, 1053, 1, false)
	}

	b.page.MustNavigate(verificationURL)
	b.page.MustWaitLoad()
	time.Sleep(3 * time.Second)

	selectors := []string{
		".qwen-chat-btn",
		"button.qwen-chat-btn",
		"[class*='confirm']",
		"button[type='submit']",
	}

	var confirmBtn *rod.Element
	var err error
	for _, sel := range selectors {
		confirmBtn, err = b.page.Timeout(5 * time.Second).Element(sel)
		if err == nil && confirmBtn != nil {
			break
		}
	}

	if confirmBtn == nil {
		confirmBtn, err = b.page.Timeout(10*time.Second).ElementR("button", "Confirm|确认|Allow")
		if err != nil {
			return fmt.Errorf("find confirm button: %w", err)
		}
	}

	confirmBtn.MustClick()
	time.Sleep(3 * time.Second)
	return nil
}
