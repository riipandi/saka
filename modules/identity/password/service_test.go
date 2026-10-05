package password

import "testing"

func TestValidateAdmitsAMixedClassCredential(t *testing.T) {
	for _, clearText := range []string{
		"V3tt0r1a!pass",
		"long enough passphrase with no digits", // length stands in for classes
		"Expecto-9atronum",
	} {
		if err := Validate(clearText); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", clearText, err)
		}
	}
}

func TestValidateRefusesAWeakCredential(t *testing.T) {
	for _, clearText := range []string{
		"123",       // too short
		"12345678",  // digits only
		"aaaaaaaa",  // one class
		"abcdefg1",  // lowercase + digit only
		"ABCDEFGH",  // uppercase only
		"pass word", // lowercase + space only
	} {
		if err := Validate(clearText); err == nil {
			t.Errorf("Validate(%q) = nil, want an error", clearText)
		}
	}
}
