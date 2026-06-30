package config

import "testing"

func TestValidateImageURL(t *testing.T) {
	c := &Config{EnableHTTPSImages: true}
	good := []string{
		"http://1.2.3.4:8080/images/x.raw.zst",
		"https://example.com/img.raw",
	}
	for _, g := range good {
		if err := c.ValidateImageURL(g); err != nil {
			t.Errorf("URL %q 应该通过: %v", g, err)
		}
	}
	bad := []string{
		"", "ftp://x", "file:///etc/passwd", "://nohost", "http://", "javascript:alert(1)",
	}
	for _, b := range bad {
		if err := c.ValidateImageURL(b); err == nil {
			t.Errorf("URL %q 应该被拒绝", b)
		}
	}

	c2 := &Config{EnableHTTPSImages: false}
	if err := c2.ValidateImageURL("https://x/y"); err == nil {
		t.Error("HTTPS 关闭时应拒绝 https URL")
	}
}

func TestEnvHelpers(t *testing.T) {
	t.Setenv("BS_T_BOOL", "yes")
	if !envBool("BS_T_BOOL", false) {
		t.Error("yes 应为 true")
	}
	t.Setenv("BS_T_BOOL", "0")
	if envBool("BS_T_BOOL", true) {
		t.Error("0 应为 false")
	}
	t.Setenv("BS_T_INT", "42")
	if envInt("BS_T_INT", 1) != 42 {
		t.Error("envInt 42")
	}
	t.Setenv("BS_T_INT", "bad")
	if envInt("BS_T_INT", 7) != 7 {
		t.Error("非法 int 应回退默认值")
	}
}
