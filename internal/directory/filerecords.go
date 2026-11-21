package directory

import (
	"encoding/hex"

	"github.com/wangzw/peers/internal/directory/directorypb"
)

// toFileRecords converts stored records to their file form. The records are
// ordered by publisher bytes, kind and object; the input slice is not modified.
// Source is the source fingerprint's lowercase hexadecimal form, or "" for a
// locally issued record.
func toFileRecords(recs []*storedRecord) []fileRecord {
	sorted := append([]*storedRecord(nil), recs...)
	sortRecords(sorted)

	out := make([]fileRecord, 0, len(sorted))
	for _, r := range sorted {
		source := ""
		if !r.source.IsZero() {
			source = r.source.String()
		}
		out = append(out, fileRecord{Raw: r.raw, Signature: r.sig, Source: source})
	}
	return out
}

// fromFileRecords parses each file entry with parseRecord and sets its source.
// An entry whose Source is neither "" nor 64 lowercase hexadecimal digits, or
// that fails parseRecord, is skipped and counted in dropped; it is never
// treated as a locally issued record. Expiry is not checked here.
func fromFileRecords(frs []fileRecord) (recs []*storedRecord, dropped int) {
	for _, fr := range frs {
		var source Fingerprint
		if fr.Source != "" {
			if !validSourceHex(fr.Source) {
				dropped++
				continue
			}
			b, err := hex.DecodeString(fr.Source)
			if err != nil {
				dropped++
				continue
			}
			copy(source[:], b)
		}

		r, err := parseRecord(&directorypb.Record{Raw: fr.Raw, Signature: fr.Signature})
		if err != nil {
			dropped++
			continue
		}
		r.source = source
		recs = append(recs, r)
	}
	return recs, dropped
}

// validSourceHex reports whether s is exactly 64 lowercase hexadecimal digits.
func validSourceHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
