package connector

import "testing"

func TestMaskValue(t *testing.T) {
	cases := []struct{ kind PiiKind; in, want string }{
		{PiiPhone, "13812341234", "138****1234"},
		{PiiEmail, "alice@example.com", "a***@example.com"},
		{PiiName, "张三丰", "张**"},
		{PiiName, "Bob", "B**"},
		{PiiNationalID, "110101199003078888", "110101********8888"},
		{PiiBankCard, "6222021234567890", "6222********7890"},
		{PiiCustom, "secret", "******"},
	}
	for _, c := range cases {
		if got := MaskValue(c.kind, c.in); got != c.want {
			t.Errorf("MaskValue(%s,%q)=%q want %q", c.kind, c.in, got, c.want)
		}
	}
}

func TestGeneralize(t *testing.T) {
	if got := GeneralizeAge("34"); got != "30-40" {
		t.Errorf("age: %q", got)
	}
	if got := GeneralizeAge("7"); got != "0-10" {
		t.Errorf("age: %q", got)
	}
	if got := GeneralizeValue(PiiDOB, "1990-03-07"); got != "1990-03" {
		t.Errorf("dob: %q", got)
	}
}

func TestRedact(t *testing.T) {
	if Redact("anything") != "[REDACTED]" {
		t.Fatal("redact")
	}
}
