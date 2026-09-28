package book

import (
	"net/url"
	"strings"
)

var trackingParams = map[string]bool{
	"fbclid": true, "gclid": true, "gclsrc": true, "dclid": true, "gbraid": true, "wbraid": true,
	"msclkid": true, "twclid": true, "ttclid": true, "igshid": true, "igsh": true, "yclid": true,
	"ysclid": true, "srsltid": true, "si": true, "mkt_tok": true, "ml_subscriber": true,
	"ml_subscriber_hash": true, "rb_clickid": true, "s_cid": true, "s_kwcid": true, "wickedid": true,
	"_openstat": true, "ref_src": true, "ref_url": true, "spm": true, "scm": true, "_branch_match_id": true,
	"_kx": true, "epik": true, "li_fat_id": true, "cmpid": true, "ncid": true, "_ga": true, "_gl": true,
}

var trackingPrefixes = []string{"utm_", "mtm_", "pk_", "piwik_", "hsa_", "_hs", "__hs", "mc_", "oly_", "vero_"}

// CleanURL strips tracking parameters and preserves the order of other fields.
func CleanURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	var kept []string
	for _, pair := range strings.Split(u.RawQuery, "&") {
		key, _, _ := strings.Cut(pair, "=")
		if decoded, err := url.QueryUnescape(key); err == nil {
			key = decoded
		}
		key = strings.ToLower(key)
		tracking := trackingParams[key]
		for _, prefix := range trackingPrefixes {
			tracking = tracking || strings.HasPrefix(key, prefix)
		}
		if !tracking {
			kept = append(kept, pair)
		}
	}
	u.RawQuery = strings.Join(kept, "&")
	return u.String()
}
