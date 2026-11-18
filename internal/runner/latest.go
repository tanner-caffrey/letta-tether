package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// LatestNPM asks npm for the newest Letta Code release (what `npm install -g` would get).
func LatestNPM(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://registry.npmjs.org/@letta-ai/letta-code/latest", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("npm said %s", resp.Status)
	}
	var v struct{ Version string }
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// Older reports whether version a is older than b ("0.34.26" < "0.34.64").
// A prerelease ("0.35.0-beta.1") comes before its release and after everything older.
// Anything it can't read counts as not older, so we never nag by mistake.
func Older(a, b string) bool {
	pa, pb := parts(a), parts(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	// same release numbers: only a prerelease vs the release differs
	return isPre(a) && !isPre(b)
}

func isPre(v string) bool { return strings.Contains(strings.TrimSpace(v), "-") }

func parts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v = strings.SplitN(v, "-", 2)[0]
	f := strings.Split(v, ".")
	if len(f) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, s := range f {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}
