package sealbox

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	plain := []byte("{\"providers\":{\"x\":{}}}\n")
	blob, err := Encrypt("hunter2", plain)
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(blob) {
		t.Fatalf("blob not marked encrypted: %q", blob)
	}
	got, err := Decrypt("hunter2", blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestWrongPasswordFails(t *testing.T) {
	blob, err := Encrypt("hunter2", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt("wrong", blob); err == nil {
		t.Fatal("wrong password should fail")
	}
}

func TestIsEncryptedPlaintext(t *testing.T) {
	if IsEncrypted([]byte("{\"providers\":{}}")) {
		t.Fatal("plaintext marked encrypted")
	}
	if !IsEncrypted([]byte("STB1:AAAA")) {
		t.Fatal("magic prefix not detected")
	}
}
