package main

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestValidatePassword(t *testing.T) {
	for _, test := range []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "valid", password: "correct horse battery staple"},
		{name: "empty", wantErr: true},
		{name: "too long", password: strings.Repeat("x", 73), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validatePassword(test.password); (err != nil) != test.wantErr {
				t.Fatalf("validatePassword() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestGeneratedHashCanAuthenticate(t *testing.T) {
	password := "janus-admin-test"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		t.Fatalf("generated hash does not authenticate password: %v", err)
	}
}
