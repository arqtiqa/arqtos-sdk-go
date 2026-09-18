package trackerevent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

func VerifyHMACSHA256(raw Raw, secret []byte) (Verified, error) {
	if len(secret) == 0 {
		return Verified{}, cerr.New(cerr.KindInvalid, op, errors.New("a non-empty secret is required"))
	}
	if err := CheckBodySize(raw.Body); err != nil {
		return Verified{}, err
	}
	want, err := hex.DecodeString(raw.Signature)
	if err != nil || len(want) != sha256.Size {
		return Verified{}, cerr.New(cerr.KindUnauthorized, op, errors.New("signature is missing or malformed"))
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(raw.Body)
	if !hmac.Equal(mac.Sum(nil), want) {
		return Verified{}, cerr.New(cerr.KindUnauthorized, op, errors.New("signature mismatch"))
	}
	return Verified{PayloadVersion: raw.PayloadVersion, Body: raw.Body}, nil
}
