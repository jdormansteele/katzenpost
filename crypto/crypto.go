// crypto.go - Reunion Cryptographic core library sans IO.
// Copyright (C) 2019  David Stainton.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

// Package provides core cryptographic functions for the Reunion protocol.
package crypto

import (
	"encoding/binary"
	"errors"
	"time"

	"crypto/sha256"
	"github.com/katzenpost/chacha20poly1305"
	"github.com/katzenpost/core/crypto/rand"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

const (
	// PayloadSize is the size of the Reunion protocol payload.
	PayloadSize = 4096

	// SymmetricKeySize is the size of the symmetric keys we use.
	SymmetricKeySize = 32

	t1AlphaSize = SPRPMinimumBlockLenth
	t1BetaSize  = SymmetricKeySize + chacha20poly1305.NonceSize + chacha20poly1305.Overhead

	// Type1MessageSize is the size in byte of the Type 1 Message.
	Type1MessageSize = t1AlphaSize + t1BetaSize + PayloadSize
)

var ErrInvalidMessageSize = errors.New("invalid message size")

func padMessage(message []byte) ([]byte, error) {
	if len(message) > PayloadSize-4 {
		return nil, ErrInvalidMessageSize
	}
	payload := make([]byte, PayloadSize)
	binary.BigEndian.PutUint32(payload[:4], uint32(len(message)))
	copy(payload[4:], message)
	return payload, nil
}

func kdf(commonReferenceString []byte, passphrase []byte, epoch uint64) ([]byte, []byte, error) {
	hashFunc := sha256.New
	salt := commonReferenceString
	// XXX t := uint32(9001)
	t := uint32(1)
	memory := uint32(9001)
	threads := uint8(1)
	keyLen := uint32(32)
	keyStretched := argon2.IDKey(passphrase, salt, t, memory, threads, keyLen)
	prk1 := hkdf.Extract(hashFunc, keyStretched, salt)

	// XXX should we also bind the Reunion server identity?
	hkdfContext1 := []byte("type 1")
	var rawEpoch [8]byte
	binary.BigEndian.PutUint64(rawEpoch[:], epoch)
	hkdfContext1 = append(hkdfContext1, rawEpoch[:]...)

	kdfReader := hkdf.Expand(hashFunc, prk1, hkdfContext1)
	key := [SPRPKeyLength]byte{}
	_, err := kdfReader.Read(key[:])
	if err != nil {
		return nil, nil, err
	}
	iv := [SPRPIVLength]byte{}
	_, err = kdfReader.Read(iv[:])
	if err != nil {
		return nil, nil, err
	}
	return key[:], iv[:], nil
}

// getLatestMidnight returns the big endian byte slice of the
// unix epoch seconds since the recent UTC midnight.
func getLatestMidnight() []byte {
	y, m, d := time.Now().Date()
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	unixSecs := t.Unix()
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], uint64(unixSecs))
	return tmp[:]
}

// getCommonReferenceString returns the common reference string.
// CRS = GMT_MIDNIGHT || SharedRandom || EpochID
// XXX TODO: append the Reunion server instance ID.
func getCommonReferenceString(sharedRandomValue []byte, epoch uint64) []byte {
	out := []byte{}
	out = append(out, getLatestMidnight()...)
	out = append(out, sharedRandomValue...)
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], epoch)
	out = append(out, tmp[:]...)
	return out
}

func newT1Alpha(epoch uint64, sharedRandomValue []byte, passphrase []byte, elligatorPubKey *[32]byte) ([]byte, error) {
	crs := getCommonReferenceString(sharedRandomValue, epoch)
	k1, k1iv, err := kdf(crs, passphrase, epoch)
	if err != nil {
		return nil, err
	}

	key := [SPRPKeyLength]byte{}
	copy(key[:], k1)
	iv := [SPRPIVLength]byte{}
	copy(iv[:], k1iv)
	alpha := SPRPEncrypt(&key, &iv, elligatorPubKey[:])
	return alpha, nil
}

func newT1Beta(elligatorPubKey, secretKey *[32]byte) ([]byte, error) {
	aead1, err := chacha20poly1305.New(secretKey[:])
	if err != nil {
		return nil, err
	}
	ad := []byte{}
	nonce1 := [chacha20poly1305.NonceSize]byte{}
	_, err = rand.Reader.Read(nonce1[:])
	if err != nil {
		return nil, err
	}
	beta := []byte{}
	beta = aead1.Seal(beta, nonce1[:], elligatorPubKey[:], ad)
	beta = append(beta, nonce1[:]...)
	return beta, nil
}

func newT1Gamma(payload []byte, secretKey *[32]byte) ([]byte, error) {
	aead2, err := chacha20poly1305.New(secretKey[:])
	if err != nil {
		return nil, err
	}
	nonce2 := [chacha20poly1305.NonceSize]byte{}
	_, err = rand.Reader.Read(nonce2[:])
	if err != nil {
		return nil, err
	}
	gamma := []byte{}
	ad := []byte{}
	gamma = aead2.Seal(gamma, nonce2[:], payload, ad)
	gamma = append(gamma, nonce2[:]...)
	return gamma, nil
}

// decodeT1Message upon success returns alpha, beta, gamma
func decodeT1Message(message []byte) ([]byte, []byte, []byte, error) {
	if len(message) != Type1MessageSize {
		return nil, nil, nil, errors.New("t1 message has invalid length")
	}
	alpha := message[:t1AlphaSize]
	beta := message[t1AlphaSize : t1AlphaSize+t1BetaSize]
	gamma := message[t1AlphaSize+t1BetaSize:]
	return alpha, beta, gamma, nil
}
