package pairing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

const (
	// oneTimeKeyLifetime is how long a generated pairing key stays usable. It is
	// a constant, not a configuration value.
	oneTimeKeyLifetime = 10 * time.Minute
	// oneTimeKeyBytes is the size of the random value behind a one-time key:
	// 80 bits, encoded as 16 Crockford Base32 characters.
	oneTimeKeyBytes = 10
	// crockfordAlphabet is the Crockford Base32 alphabet used for one-time keys.
	crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	// pairHintLabel is the HMAC label for the lookup tag. It has no NUL
	// terminator.
	pairHintLabel = "agent-peer pair hint"
)

// oneTimeKey is one unexpired key and its verification failure count.
type oneTimeKey struct {
	key      string
	expires  time.Time
	failures int
}

// keyMatch is the key selected for one handshake. oneTime distinguishes a
// one-time key from the preset static key, and proof carries the initiator's
// PairConfirm proof until verification.
type keyMatch struct {
	key     string
	oneTime bool
	index   int
	proof   []byte
}

// attemptCounter counts, for one minute, pairing requests that matched neither a
// one-time key nor the preset static key.
type attemptCounter struct {
	minute time.Time
	count  int
}

// keyTable holds the one-time keys and the per-minute attempt counter. One
// mutex covers both. Verification and removal of a one-time key happen under
// the same lock, so two concurrent handshakes cannot both consume one key.
type keyTable struct {
	mu       sync.Mutex
	keys     []oneTimeKey
	attempts attemptCounter
}

// encodeOneTimeKey renders 10 random bytes as four groups of four Crockford
// Base32 characters separated by '-'. Ten zero bytes encode as
// "0000-0000-0000-0000".
func encodeOneTimeKey(raw [10]byte) string {
	var chars [16]byte
	for i := range chars {
		var v byte
		for b := 0; b < 5; b++ {
			bit := i*5 + b
			v = v<<1 | (raw[bit/8]>>uint(7-bit%8))&1
		}
		chars[i] = crockfordAlphabet[v]
	}
	out := make([]byte, 0, 19)
	for group := 0; group < 4; group++ {
		if group > 0 {
			out = append(out, '-')
		}
		out = append(out, chars[group*4:(group+1)*4]...)
	}
	return string(out)
}

// computeHint is the lookup tag the initiator sends in PairRequest. It lets the
// acceptor find the key without revealing it, and has no NUL terminator.
func computeHint(key, ni []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(pairHintLabel))
	mac.Write(ni)
	return mac.Sum(nil)[:lookupTagLength]
}

// add stores a new one-time key that expires oneTimeKeyLifetime from now, after
// dropping keys that have already expired.
func (k *keyTable) add(key string, now time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys = purgeExpired(k.keys, now)
	k.keys = append(k.keys, oneTimeKey{key: key, expires: now.Add(oneTimeKeyLifetime)})
}

// purgeExpired returns the keys that are still valid at now.
func purgeExpired(keys []oneTimeKey, now time.Time) []oneTimeKey {
	kept := keys[:0]
	for _, key := range keys {
		if now.Before(key.expires) {
			kept = append(kept, key)
		}
	}
	return kept
}

// lookupAtRequest selects the key for an incoming PairRequest. It first looks
// for a matching one-time key, then checks the per-minute attempt limit, then
// the preset static key. A request that matches nothing increments the counter.
// found is false on rejection; the returned error is a PairingError whose reason
// maps to the PairReject reason.
//
// The per-key failure limit is not consulted here: recordVerificationFailure
// already discards a one-time key once its failures reach that limit, so any key
// still in the table is eligible.
func (k *keyTable) lookupAtRequest(ni, hint []byte, now time.Time, staticKey string, maxAttempts int) (keyMatch, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.keys = purgeExpired(k.keys, now)
	for i := range k.keys {
		if hmac.Equal(computeHint([]byte(k.keys[i].key), ni), hint) {
			return keyMatch{key: k.keys[i].key, oneTime: true, index: i}, true, nil
		}
	}

	// Reset the counter when the minute changes before consulting the limit;
	// otherwise a minute that reached the limit would reject forever.
	minute := now.Truncate(time.Minute)
	if !k.attempts.minute.Equal(minute) {
		k.attempts = attemptCounter{minute: minute}
	}

	if k.attempts.count >= maxAttempts {
		return keyMatch{}, false, &PairingError{Reason: ReasonTryLater, Detail: rejectDetail(ReasonTryLater)}
	}

	if staticKey != "" && hmac.Equal(computeHint([]byte(staticKey), ni), hint) {
		return keyMatch{key: staticKey}, true, nil
	}

	k.attempts.count++
	return keyMatch{}, false, &PairingError{Reason: ReasonKeyInvalid, Detail: rejectDetail(ReasonKeyInvalid)}
}

// verifyAtConfirm checks the initiator's proof against the key selected at
// request time. A one-time key must still be in the table and unexpired; when it
// is gone or expired, keyGone is true and the caller answers KEY_INVALID without
// counting a failure. A successful one-time key is removed atomically with the
// check.
func (k *keyTable) verifyAtConfirm(m keyMatch, ni, nr []byte, acceptorFP, initiatorSeen Fingerprint, staticKey string, now time.Time) (ok, keyGone bool) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if m.oneTime {
		idx := -1
		for i := range k.keys {
			if k.keys[i].key == m.key {
				idx = i
				break
			}
		}
		if idx < 0 || !now.Before(k.keys[idx].expires) {
			if idx >= 0 {
				k.keys = append(k.keys[:idx], k.keys[idx+1:]...)
			}
			return false, true
		}
		want := initiatorProof([]byte(m.key), ni, nr, initiatorSeen, acceptorFP)
		if !hmac.Equal(want, m.proof) {
			return false, false
		}
		k.keys = append(k.keys[:idx], k.keys[idx+1:]...)
		return true, false
	}

	// The preset static key must still equal its current configured value. A
	// rotated or deleted key cannot finish a handshake that started before it.
	if staticKey == "" || staticKey != m.key {
		return false, false
	}
	want := initiatorProof([]byte(staticKey), ni, nr, initiatorSeen, acceptorFP)
	return hmac.Equal(want, m.proof), false
}

// recordVerificationFailure counts one failed verification against a one-time
// key and discards the key once maxFailures is reached. The static key is never
// counted.
func (k *keyTable) recordVerificationFailure(m keyMatch, maxFailures int, now time.Time) {
	if !m.oneTime {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()

	for i := range k.keys {
		if k.keys[i].key != m.key {
			continue
		}
		k.keys[i].failures++
		if maxFailures <= 0 || k.keys[i].failures >= maxFailures {
			k.keys = append(k.keys[:i], k.keys[i+1:]...)
		}
		return
	}
}

// CreateOneTimeKey generates a fresh one-time pairing key, returns its expiry
// and the display addresses of this Broker. It refuses to generate a key when no
// listen address is configured, because the peer could not dial this Broker.
func (p *Pairing) CreateOneTimeKey() (key string, expires time.Time, shown []string, err error) {
	if p == nil || p.settings == nil || p.clock == nil || p.keys == nil {
		return "", time.Time{}, nil, ErrNotOpen
	}
	listen, err := p.settings.ListenAddress()
	if err != nil {
		return "", time.Time{}, nil, fmt.Errorf("%w: %v", ErrStore, err)
	}
	if listen == "" {
		return "", time.Time{}, nil, ErrNotListening
	}

	var raw [oneTimeKeyBytes]byte
	if err := p.randomBytes(raw[:]); err != nil {
		return "", time.Time{}, nil, err
	}
	key = encodeOneTimeKey(raw)

	advertised, err := p.settings.Advertised()
	if err != nil {
		return "", time.Time{}, nil, fmt.Errorf("%w: %v", ErrStore, err)
	}
	hostname, err := p.settings.Hostname()
	if err != nil {
		return "", time.Time{}, nil, fmt.Errorf("%w: %v", ErrStore, err)
	}
	known := filterKnown(advertised)
	shown = make([]string, 0, len(known))
	for _, addr := range known {
		shown = append(shown, hostname+"("+addr.Addr+")")
	}

	now := p.clock.Now()
	expires = now.Add(oneTimeKeyLifetime)
	p.keys.add(key, now)
	return key, expires, shown, nil
}

// randomBytes fills buf from the injected random source, falling back to
// crypto/rand.
func (p *Pairing) randomBytes(buf []byte) error {
	if p.randRead != nil {
		_, err := p.randRead(buf)
		return err
	}
	_, err := rand.Read(buf)
	return err
}
