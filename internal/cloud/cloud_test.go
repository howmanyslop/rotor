package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient wires a Client to an httptest server with retry/poll delays
// shrunk to keep backoff-exercising tests fast.
func newTestClient(t *testing.T, handler http.Handler, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("test-key", append([]Option{WithBaseURL(srv.URL)}, opts...)...)
	c.retryBase = time.Millisecond
	c.retryCap = 5 * time.Millisecond
	c.pollBase = time.Millisecond
	c.pollCap = 5 * time.Millisecond
	return c
}

func TestAuthAndUserAgentHeaders(t *testing.T) {
	var gotKey, gotUA string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, `{"displayName":"u"}`)
	}), WithUserAgent("rotor-test/1"))

	if _, err := c.GetUniverse(context.Background(), 1); err != nil {
		t.Fatalf("GetUniverse: %v", err)
	}
	if gotKey != "test-key" {
		t.Errorf("x-api-key = %q, want %q", gotKey, "test-key")
	}
	if gotUA != "rotor-test/1" {
		t.Errorf("User-Agent = %q, want %q", gotUA, "rotor-test/1")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("ROBLOX_API_KEY", "")
	if _, err := FromEnv(); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("FromEnv with empty env: err = %v, want ErrNoAPIKey", err)
	}
	t.Setenv("ROBLOX_API_KEY", "abc")
	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.apiKey != "abc" {
		t.Errorf("apiKey = %q, want %q", c.apiKey, "abc")
	}
	if c.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultBaseURL)
	}
}

// retries: table over status codes that must be retried then succeed.
func TestRetriesThenSuccess(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		retryAfter string // optional Retry-After header on the failure
		failures   int
	}{
		{name: "429 with Retry-After", status: 429, retryAfter: "0", failures: 2},
		{name: "500", status: 500, failures: 1},
		{name: "503", status: 503, failures: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if int(calls.Add(1)) <= tt.failures {
					if tt.retryAfter != "" {
						w.Header().Set("Retry-After", tt.retryAfter)
					}
					w.WriteHeader(tt.status)
					fmt.Fprint(w, `{"code":"THROTTLED","message":"slow down"}`)
					return
				}
				fmt.Fprint(w, `{"displayName":"ok"}`)
			}))

			u, err := c.GetUniverse(context.Background(), 1)
			if err != nil {
				t.Fatalf("GetUniverse after %d failures: %v", tt.failures, err)
			}
			if u.DisplayName != "ok" {
				t.Errorf("DisplayName = %q, want %q", u.DisplayName, "ok")
			}
			if got, want := int(calls.Load()), tt.failures+1; got != want {
				t.Errorf("server saw %d calls, want %d", got, want)
			}
		})
	}
}

func TestRetriesExhausted(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
		fmt.Fprint(w, `{"code":"INTERNAL","message":"boom"}`)
	}))

	_, err := c.GetUniverse(context.Background(), 1)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != 500 || apiErr.Code != "INTERNAL" {
		t.Errorf("APIError = %+v, want StatusCode 500 Code INTERNAL", apiErr)
	}
	if got := int(calls.Load()); got != c.maxAttempts {
		t.Errorf("server saw %d calls, want maxAttempts = %d", got, c.maxAttempts)
	}
}

func TestBadRequestNotRetried(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(400)
		fmt.Fprint(w, `{"code":"INVALID_ARGUMENT","message":"displayName is required"}`)
	}))

	_, err := c.GetUniverse(context.Background(), 1)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
	if apiErr.Code != "INVALID_ARGUMENT" || apiErr.Message != "displayName is required" {
		t.Errorf("parsed error = %+v, want INVALID_ARGUMENT/displayName is required", apiErr)
	}
	if calls.Load() != 1 {
		t.Errorf("server saw %d calls, want 1 (no retries on 400)", calls.Load())
	}
}

func TestParseAPIErrorShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
		msg  string
	}{
		{"cloud v2", `{"code":"NOT_FOUND","message":"nope"}`, "NOT_FOUND", "nope"},
		{"error field", `{"error":"NotFound","message":"nope"}`, "NotFound", "nope"},
		{"legacy errors array", `{"errors":[{"code":1,"message":"Badge not found"}]}`, "1", "Badge not found"},
		{"garbage body", `<html>`, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := parseAPIError(404, []byte(tt.body))
			if e.StatusCode != 404 || e.Code != tt.code || e.Message != tt.msg {
				t.Errorf("parseAPIError = %+v, want code %q msg %q", e, tt.code, tt.msg)
			}
		})
	}
}

func TestCreateAssetMultipart(t *testing.T) {
	fileBytes := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3}
	var gotReqJSON, gotFile []byte
	var gotFileName, gotFileCT string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/assets/v1/assets" {
			t.Errorf("got %s %s, want POST /assets/v1/assets", r.Method, r.URL.Path)
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("Content-Type = %q (%v), want multipart/form-data", r.Header.Get("Content-Type"), err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("NextPart: %v", err)
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "request":
				gotReqJSON = data
			case "fileContent":
				gotFile = data
				gotFileName = part.FileName()
				gotFileCT = part.Header.Get("Content-Type")
			default:
				t.Errorf("unexpected part %q", part.FormName())
			}
		}
		fmt.Fprint(w, `{"path":"operations/op-1","done":false}`)
	}))

	req := CreateAssetRequest{
		AssetType:       "Decal",
		DisplayName:     "logo",
		Description:     "rotor logo",
		CreationContext: CreationContext{Creator: Creator{GroupID: 4500}},
	}
	opPath, err := c.CreateAsset(context.Background(), req, "logo.png", bytes.NewReader(fileBytes))
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}
	if opPath != "operations/op-1" {
		t.Errorf("operationPath = %q, want operations/op-1", opPath)
	}

	var decoded CreateAssetRequest
	if err := json.Unmarshal(gotReqJSON, &decoded); err != nil {
		t.Fatalf("request part is not JSON: %v\n%s", err, gotReqJSON)
	}
	if decoded != req {
		t.Errorf("request part = %+v, want %+v", decoded, req)
	}
	// int64 creator ids must serialize as JSON strings (proto3 mapping).
	if !strings.Contains(string(gotReqJSON), `"groupId":"4500"`) {
		t.Errorf("request JSON %s: groupId not serialized as string", gotReqJSON)
	}
	if !bytes.Equal(gotFile, fileBytes) {
		t.Errorf("fileContent part = %v, want %v", gotFile, fileBytes)
	}
	if gotFileName != "logo.png" {
		t.Errorf("filename = %q, want logo.png", gotFileName)
	}
	if gotFileCT != "image/png" {
		t.Errorf("fileContent Content-Type = %q, want image/png", gotFileCT)
	}
}

func TestUpdateAssetContent(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/assets/v1/assets/777" {
			t.Errorf("got %s %s, want PATCH /assets/v1/assets/777", r.Method, r.URL.Path)
		}
		mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "multipart/form-data" {
			t.Fatalf("Content-Type = %q, want multipart/form-data", mediaType)
		}
		form, err := multipart.NewReader(r.Body, params["boundary"]).ReadForm(1 << 20)
		if err != nil {
			t.Fatalf("ReadForm: %v", err)
		}
		if got := form.Value["request"]; len(got) != 1 || got[0] != `{"assetId":"777"}` {
			t.Errorf("request part = %q, want {\"assetId\":\"777\"}", got)
		}
		fmt.Fprint(w, `{"path":"operations/op-2","done":false}`)
	}))

	opPath, err := c.UpdateAssetContent(context.Background(), 777, "logo.png", strings.NewReader("png-bytes"))
	if err != nil {
		t.Fatalf("UpdateAssetContent: %v", err)
	}
	if opPath != "operations/op-2" {
		t.Errorf("operationPath = %q, want operations/op-2", opPath)
	}
}

func TestPollOperation(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assets/v1/operations/op-1" {
			t.Errorf("path = %s, want /assets/v1/operations/op-1", r.URL.Path)
		}
		if calls.Add(1) < 3 {
			fmt.Fprint(w, `{"path":"operations/op-1","done":false}`)
			return
		}
		fmt.Fprint(w, `{"path":"operations/op-1","done":true,"response":{"assetId":"123","displayName":"logo"}}`)
	}))

	var asset Asset
	if err := c.PollOperation(context.Background(), "operations/op-1", &asset); err != nil {
		t.Fatalf("PollOperation: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("server saw %d polls, want 3", calls.Load())
	}
	if asset.AssetID != 123 || asset.DisplayName != "logo" {
		t.Errorf("decoded asset = %+v, want AssetID 123 DisplayName logo", asset)
	}
}

func TestPollOperationError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"path":"operations/op-1","done":true,"error":{"code":"MODERATED","message":"asset rejected"}}`)
	}))

	err := c.PollOperation(context.Background(), "operations/op-1", &Asset{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Code != "MODERATED" || apiErr.Message != "asset rejected" {
		t.Errorf("operation error = %+v, want MODERATED/asset rejected", apiErr)
	}
}

func TestPollOperationContextCancel(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"path":"operations/op-1","done":false}`)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := c.PollOperation(ctx, "operations/op-1", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestPublishPlaceVersion(t *testing.T) {
	placeBytes := []byte("<roblox place bytes>")
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/universes/v1/11/places/22/versions" {
			t.Errorf("got %s %s, want POST /universes/v1/11/places/22/versions", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("versionType"); got != "Published" {
			t.Errorf("versionType = %q, want Published", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("Content-Type = %q, want application/octet-stream", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, placeBytes) {
			t.Errorf("body = %q, want %q", body, placeBytes)
		}
		fmt.Fprint(w, `{"versionNumber":42}`)
	}))

	v, err := c.PublishPlaceVersion(context.Background(), 11, 22, VersionTypePublished, bytes.NewReader(placeBytes))
	if err != nil {
		t.Fatalf("PublishPlaceVersion: %v", err)
	}
	if v != 42 {
		t.Errorf("versionNumber = %d, want 42", v)
	}
}

func TestUpdateUniverseMaskAndMethod(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/cloud/v2/universes/99" {
			t.Errorf("got %s %s, want PATCH /cloud/v2/universes/99", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("updateMask"); got != "displayName,visibility" {
			t.Errorf("updateMask = %q, want displayName,visibility", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		var u Universe
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		u.Path = "universes/99"
		json.NewEncoder(w).Encode(u)
	}))

	in := Universe{DisplayName: "My Game", Visibility: "PUBLIC"}
	out, err := c.UpdateUniverse(context.Background(), 99, in, []string{"displayName", "visibility"})
	if err != nil {
		t.Fatalf("UpdateUniverse: %v", err)
	}
	if out.DisplayName != "My Game" || out.Path != "universes/99" {
		t.Errorf("updated universe = %+v", out)
	}
}

func TestGetPlace(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/cloud/v2/universes/1/places/2" {
			t.Errorf("got %s %s, want GET /cloud/v2/universes/1/places/2", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"path":"universes/1/places/2","displayName":"start","serverSize":50}`)
	}))

	p, err := c.GetPlace(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("GetPlace: %v", err)
	}
	if p.DisplayName != "start" || p.ServerSize != 50 {
		t.Errorf("place = %+v, want start/50", p)
	}
}

func TestRateLimiterConcurrentNoDeadlock(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{}`)
	}), WithRateLimit(2000, 1)) // burst 1 forces every call through the wait path

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const n = 20
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.GetUniverse(ctx, int64(i))
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("rate limiter deadlocked: 20 concurrent calls did not finish")
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
	if calls.Load() != n {
		t.Errorf("server saw %d calls, want %d", calls.Load(), n)
	}
}

func TestBadgeAndGamePassEndpoints(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /legacy-badges/v1/universes/5/badges":
			fmt.Fprint(w, `{"id":900,"name":"Winner","enabled":true}`)
		case "PATCH /legacy-badges/v1/badges/900":
			fmt.Fprint(w, `{"id":900,"name":"Champion","enabled":true}`)
		case "POST /legacy-game-passes/v1/universes/5/game-passes":
			fmt.Fprint(w, `{"gamePassId":33,"name":"VIP","price":100,"isForSale":true}`)
		case "PATCH /legacy-game-passes/v1/game-passes/33":
			fmt.Fprint(w, `{"gamePassId":33,"name":"VIP+","price":150,"isForSale":true}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	ctx := context.Background()

	b, err := c.CreateBadge(ctx, 5, CreateBadgeRequest{Name: "Winner"})
	if err != nil || b.ID != 900 {
		t.Errorf("CreateBadge = %+v, %v", b, err)
	}
	b, err = c.UpdateBadge(ctx, 900, UpdateBadgeRequest{Name: "Champion"})
	if err != nil || b.Name != "Champion" {
		t.Errorf("UpdateBadge = %+v, %v", b, err)
	}
	price := int64(100)
	g, err := c.CreateGamePass(ctx, 5, CreateGamePassRequest{Name: "VIP", Price: &price})
	if err != nil || g.GamePassID != 33 {
		t.Errorf("CreateGamePass = %+v, %v", g, err)
	}
	g, err = c.UpdateGamePass(ctx, 33, UpdateGamePassRequest{Name: "VIP+"})
	if err != nil || g.Price != 150 {
		t.Errorf("UpdateGamePass = %+v, %v", g, err)
	}
}

func TestPublishingEndpoints(t *testing.T) {
	var iconBody []byte
	var iconCT string
	var orderBody []byte
	var deleted []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /universes/v1/7/icon":
			iconCT = r.Header.Get("Content-Type")
			iconBody, _ = io.ReadAll(r.Body)
			fmt.Fprint(w, `{"targetId":1111}`)
		case "POST /universes/v1/7/thumbnails":
			fmt.Fprint(w, `{"targetId":2222}`)
		case "POST /universes/v1/7/thumbnails/order":
			orderBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(200)
		case "DELETE /universes/v1/7/thumbnails/2222":
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	ctx := context.Background()

	id, err := c.UploadUniverseIcon(ctx, 7, "icon.png", strings.NewReader("png-bytes"))
	if err != nil || id != 1111 {
		t.Errorf("UploadUniverseIcon = %d, %v", id, err)
	}
	mediaType, params, err := mime.ParseMediaType(iconCT)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("icon Content-Type = %q (%v), want multipart/form-data", iconCT, err)
	}
	form, err := multipart.NewReader(bytes.NewReader(iconBody), params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("icon multipart: %v", err)
	}
	files := form.File["file"]
	if len(files) != 1 || files[0].Filename != "icon.png" {
		t.Errorf("icon file part = %+v, want one part named file/icon.png", files)
	}

	id, err = c.UploadUniverseThumbnail(ctx, 7, "thumb.png", strings.NewReader("png-bytes"))
	if err != nil || id != 2222 {
		t.Errorf("UploadUniverseThumbnail = %d, %v", id, err)
	}
	if err := c.SetUniverseThumbnailOrder(ctx, 7, []int64{2222, 1111}); err != nil {
		t.Errorf("SetUniverseThumbnailOrder: %v", err)
	}
	if got := strings.TrimSpace(string(orderBody)); got != `{"thumbnailIds":[2222,1111]}` {
		t.Errorf("order body = %s", got)
	}
	if err := c.DeleteUniverseThumbnail(ctx, 7, 2222); err != nil || len(deleted) != 1 {
		t.Errorf("DeleteUniverseThumbnail: %v (deleted %v)", err, deleted)
	}
}

func TestDeveloperProductAndSocialLinkEndpoints(t *testing.T) {
	var productCreateBody, linkCreateBody []byte
	var linkDeleted bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /developer-products/v1/universes/9/developerproducts":
			productCreateBody, _ = io.ReadAll(r.Body)
			fmt.Fprint(w, `{"id":71,"name":"Coins","priceInRobux":25}`)
		case "PATCH /developer-products/v1/universes/9/developerproducts/71":
			fmt.Fprint(w, `{"id":71,"name":"Coins","priceInRobux":50}`)
		case "POST /legacy-develop/v1/universes/9/social-links":
			linkCreateBody, _ = io.ReadAll(r.Body)
			fmt.Fprint(w, `{"id":81,"title":"Join","url":"https://discord.gg/x","type":"Discord"}`)
		case "PATCH /legacy-develop/v1/universes/9/social-links/81":
			fmt.Fprint(w, `{"id":81,"title":"Join!","url":"https://discord.gg/x","type":"Discord"}`)
		case "DELETE /legacy-develop/v1/universes/9/social-links/81":
			linkDeleted = true
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	ctx := context.Background()

	p, err := c.CreateDeveloperProduct(ctx, 9, CreateDeveloperProductRequest{Name: "Coins", PriceInRobux: 25})
	if err != nil || p.ID != 71 {
		t.Errorf("CreateDeveloperProduct = %+v, %v", p, err)
	}
	if got := strings.TrimSpace(string(productCreateBody)); got != `{"name":"Coins","priceInRobux":25}` {
		t.Errorf("product create body = %s", got)
	}
	p, err = c.UpdateDeveloperProduct(ctx, 9, 71, UpdateDeveloperProductRequest{Name: "Coins", PriceInRobux: 50})
	if err != nil || p.PriceInRobux != 50 {
		t.Errorf("UpdateDeveloperProduct = %+v, %v", p, err)
	}

	s, err := c.CreateSocialLink(ctx, 9, SocialLinkRequest{Title: "Join", URL: "https://discord.gg/x", Type: "Discord"})
	if err != nil || s.ID != 81 {
		t.Errorf("CreateSocialLink = %+v, %v", s, err)
	}
	if got := strings.TrimSpace(string(linkCreateBody)); got != `{"title":"Join","url":"https://discord.gg/x","type":"Discord"}` {
		t.Errorf("link create body = %s", got)
	}
	s, err = c.UpdateSocialLink(ctx, 9, 81, SocialLinkRequest{Title: "Join!", URL: "https://discord.gg/x", Type: "Discord"})
	if err != nil || s.Title != "Join!" {
		t.Errorf("UpdateSocialLink = %+v, %v", s, err)
	}
	if err := c.DeleteSocialLink(ctx, 9, 81); err != nil || !linkDeleted {
		t.Errorf("DeleteSocialLink: %v (deleted %v)", err, linkDeleted)
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))
	if d := retryAfter(h); d <= 0 || d > 3*time.Second {
		t.Errorf("retryAfter(date) = %v, want ~2s", d)
	}
	h.Set("Retry-After", "7")
	if d := retryAfter(h); d != 7*time.Second {
		t.Errorf("retryAfter(7) = %v, want 7s", d)
	}
	h.Del("Retry-After")
	if d := retryAfter(h); d != 0 {
		t.Errorf("retryAfter(absent) = %v, want 0", d)
	}
}

// ResolveImageID follows the asset-delivery location to the decal's content
// XML and extracts the wrapped Image asset id.
func TestResolveImageID(t *testing.T) {
	var srvURL string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset-delivery-api/v1/assetId/111":
			fmt.Fprintf(w, `{"location":%q}`, srvURL+"/cdn/decal.xml")
		case "/cdn/decal.xml":
			fmt.Fprint(w, `<roblox><url>http://www.roblox.com/asset/?id=222</url></roblox>`)
		case "/asset-delivery-api/v1/assetId/333":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"errors":[{"code":403,"message":"forbidden"}]}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = c.baseURL

	id, err := c.ResolveImageID(context.Background(), 111)
	if err != nil {
		t.Fatalf("ResolveImageID: %v", err)
	}
	if id != 222 {
		t.Errorf("id = %d, want 222", id)
	}

	// A 403 (missing legacy-assets scope) names the scope in the error.
	if _, err := c.ResolveImageID(context.Background(), 333); err == nil {
		t.Error("expected an error for 403")
	} else if !strings.Contains(err.Error(), "legacy-assets") {
		t.Errorf("403 error should mention the legacy-assets scope, got: %v", err)
	}
}
