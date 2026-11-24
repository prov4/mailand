package dav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/emersion/go-webdav"
)

// listETags lists a collection's member resources with their ETags using a
// minimal Depth-1 PROPFIND (getetag + resourcetype). webdav.Client.ReadDir is
// not used: it fails when a server omits getcontentlength for a member.
func listETags(ctx context.Context, c webdav.HTTPClient, endpoint, coll string) (map[string]string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	u := &url.URL{Scheme: base.Scheme, Host: base.Host, Path: coll}
	const body = `<?xml version="1.0" encoding="utf-8"?>` +
		`<d:propfind xmlns:d="DAV:"><d:prop><d:getetag/><d:resourcetype/></d:prop></d:propfind>`
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", u.String(), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "1")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("PROPFIND %s: %d %s", coll, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	var ms struct {
		Responses []struct {
			Href     string `xml:"DAV: href"`
			Propstat []struct {
				Status string `xml:"DAV: status"`
				Prop   struct {
					ETag         string `xml:"DAV: getetag"`
					ResourceType struct {
						Collection *struct{} `xml:"DAV: collection"`
					} `xml:"DAV: resourcetype"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: propstat"`
		} `xml:"DAV: response"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("PROPFIND %s: %w", coll, err)
	}
	out := map[string]string{}
	self := strings.TrimSuffix(coll, "/")
	for _, r := range ms.Responses {
		hu, err := url.Parse(strings.TrimSpace(r.Href))
		if err != nil {
			continue
		}
		p := hu.Path
		if strings.TrimSuffix(p, "/") == self {
			continue
		}
		etag, isColl, ok := "", false, false
		for _, ps := range r.Propstat {
			if !strings.Contains(ps.Status, " 200") {
				continue
			}
			ok = true
			if ps.Prop.ETag != "" {
				etag = normETag(ps.Prop.ETag)
			}
			if ps.Prop.ResourceType.Collection != nil {
				isColl = true
			}
		}
		if ok && !isColl {
			out[p] = etag
		}
	}
	return out, nil
}

// normETag strips the weak prefix and quotes, so ETags from PROPFIND and from
// go-webdav's multiget (which unquotes) compare equal.
func normETag(e string) string {
	e = strings.TrimSpace(e)
	e = strings.TrimPrefix(e, "W/")
	return strings.Trim(e, `"`)
}
