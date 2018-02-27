package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HALtheWise/go-links/context"
	"github.com/syndtr/goleveldb/leveldb"
)

type urlReq struct {
	URL string `json:"url"`
}

type env struct {
	mux *http.ServeMux
	dir string
	ctx *context.Context
}

func (e *env) destroy() {
	os.RemoveAll(e.dir)
}

func (e *env) get(path string) (*mockResponse, error) {
	return e.call("GET", path, nil)
}

func (e *env) post(path string, body interface{}) (*mockResponse, error) {
	return e.callWithJSON("POST", path, body)
}

func (e *env) callWithJSON(method, path string, body interface{}) (*mockResponse, error) {
	var r io.Reader

	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
		r = &buf
	}

	return e.call(method, path, r)
}

func (e *env) call(method, path string, body io.Reader) (*mockResponse, error) {
	req, err := http.NewRequest(method, path, body)
	if err != nil {
		return nil, err
	}

	res := &mockResponse{
		header: map[string][]string{},
	}

	e.mux.ServeHTTP(res, req)

	return res, nil
}

func newEnv() (*env, error) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		return nil, err
	}

	ctx, err := context.Open(filepath.Join(dir, "data"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}

	mux := http.NewServeMux()

	Setup(mux, ctx)

	return &env{
		mux: mux,
		dir: dir,
		ctx: ctx,
	}, nil
}

func needEnv(t *testing.T) *env {
	e, err := newEnv()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

type mockResponse struct {
	header http.Header
	bytes.Buffer
	status int
}

func (r *mockResponse) Header() http.Header {
	return r.header
}

func (r *mockResponse) WriteHeader(status int) {
	r.status = status
}

func mustBeSameNamedRoute(t *testing.T, a, b *routeWithName) {
	if a.Name != b.Name || a.URL != b.URL || a.Time.UnixNano() != b.Time.UnixNano() {
		t.Fatalf("routes are not same: %v vs %v", a, b)
	}
}

func mustBeRouteOf(t *testing.T, rt *context.Route, url string) {
	if rt == nil {
		t.Fatal("route is nil")
	}

	if rt.URL != url {
		t.Fatalf("expected url of %s, got %s", url, rt.URL)
	}

	if rt.Time.IsZero() {
		t.Fatal("route time is empty")
	}
}

func mustBeNamedRouteOf(t *testing.T, rt *routeWithName, name, url string) {
	mustBeRouteOf(t, rt.Route, url)
	if rt.Name != name {
		t.Fatalf("expected name of %s, got %s", name, rt.Name)
	}
}

func mustBeOk(t *testing.T, ok bool) {
	if !ok {
		t.Fatal("response is not ok")
	}
}

func mustBeErr(t *testing.T, m *msgErr) {
	if m.Ok {
		t.Fatal("response is ok, should be err")
	}

	if m.Error == "" {
		t.Fatal("expected an Error, but it is empty")
	}
}

func mustHaveStatus(t *testing.T, res *mockResponse, status int) {
	if res.status != status {
		t.Fatalf("expected response status %d, got %d", status, res.status)
	}
}

func TestAPIGetNotFound(t *testing.T) {
	e := needEnv(t)
	defer e.destroy()

	names := map[string]int{
		"":              http.StatusBadRequest,
		"nothing":       http.StatusNotFound,
		"nothing/there": http.StatusNotFound,
	}

	for name, status := range names {
		res, err := e.get(fmt.Sprintf("/api/url/%s", name))
		if err != nil {
			t.Fatal(err)
		}

		mustHaveStatus(t, res, status)

		var m msgErr
		if err := json.NewDecoder(res).Decode(&m); err != nil {
			t.Fatal(err)
		}

		mustBeErr(t, &m)
	}
}

func TestAPIPutThenGet(t *testing.T) {
	e := needEnv(t)
	defer e.destroy()

	res, err := e.post("/api/url/xxx", &urlReq{
		URL: "http://ex.com/",
	})
	if err != nil {
		t.Fatal(err)
	}

	mustHaveStatus(t, res, http.StatusOK)

	var pm msgRoute
	if err := json.NewDecoder(res).Decode(&pm); err != nil {
		t.Fatal(err)
	}

	mustBeOk(t, pm.Ok)
	mustBeNamedRouteOf(t, pm.Route, "xxx", "http://ex.com/")

	res, err = e.get("/api/url/xxx")
	if err != nil {
		t.Fatal(err)
	}

	mustHaveStatus(t, res, http.StatusOK)

	var gm msgRoute
	if err := json.NewDecoder(res).Decode(&gm); err != nil {
		t.Fatal(err)
	}

	mustBeOk(t, gm.Ok)
	mustBeNamedRouteOf(t, pm.Route, "xxx", "http://ex.com/")
}

func TestBadPuts(t *testing.T) {
	e := needEnv(t)
	defer e.destroy()

	var m msgErr

	res, err := e.call("POST", "/api/url/yyy", bytes.NewBufferString("not json"))
	if err != nil {
		t.Fatal(err)
	}
	mustHaveStatus(t, res, http.StatusBadRequest)

	if err := json.NewDecoder(res).Decode(&m); err != nil {
		t.Fatal(err)
	}
	mustBeErr(t, &m)

	res, err = e.post("/api/url/yyy", &urlReq{})
	if err != nil {
		t.Fatal(err)
	}
	mustHaveStatus(t, res, http.StatusBadRequest)

	if err := json.NewDecoder(res).Decode(&m); err != nil {
		t.Fatal(err)
	}
	mustBeErr(t, &m)

	res, err = e.post("/api/url/yyy", &urlReq{"not a URL"})
	if err != nil {
		t.Fatal(err)
	}
	mustHaveStatus(t, res, http.StatusBadRequest)

	if err := json.NewDecoder(res).Decode(&m); err != nil {
		t.Fatal(err)
	}
	mustBeErr(t, &m)
}

func TestAPIDel(t *testing.T) {
	e := needEnv(t)
	defer e.destroy()

	if err := e.ctx.Put("xxx", &context.Route{
		URL:  "http://ex.com/",
		Time: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	res, err := e.call("DELETE", "/api/url/xxx", nil)
	if err != nil {
		t.Fatal(err)
	}

	mustHaveStatus(t, res, http.StatusOK)

	var m msg
	if err := json.NewDecoder(res).Decode(&m); err != nil {
		t.Fatal(err)
	}
	mustBeOk(t, m.Ok)

	if _, err := e.ctx.Get("xxx"); err != leveldb.ErrNotFound {
		t.Fatal("expected xxx to be deleted")
	}
}

func TestAPIPutThenGetAuto(t *testing.T) {
	e := needEnv(t)
	defer e.destroy()

	res, err := e.post("/api/url/", &urlReq{URL: "http://b.com/"})
	if err != nil {
		t.Fatal(err)
	}

	mustHaveStatus(t, res, http.StatusOK)

	var am msgRoute
	if err := json.NewDecoder(res).Decode(&am); err != nil {
		t.Fatal(err)
	}
	mustBeOk(t, am.Ok)
	mustBeRouteOf(t, am.Route.Route, "http://b.com/")

	res, err = e.get(fmt.Sprintf("/api/url/%s", am.Route.Name))
	if err != nil {
		t.Fatal(err)
	}

	mustHaveStatus(t, res, http.StatusOK)

	var bm msgRoute
	if err := json.NewDecoder(res).Decode(&bm); err != nil {
		t.Fatal(err)
	}
	mustBeOk(t, bm.Ok)
	mustBeNamedRouteOf(t, bm.Route, am.Route.Name, "http://b.com/")
}

func getInPages(e *env, params url.Values) ([][]*routeWithName, error) {
	var pages [][]*routeWithName

	for {
		res, err := e.get("/api/urls/?" + params.Encode())
		if err != nil {
			return nil, err
		}

		if res.status != http.StatusOK {
			return nil, fmt.Errorf("HTTP status: %d", res.status)
		}

		var m msgRoutes
		if err := json.NewDecoder(res).Decode(&m); err != nil {
			return nil, err
		}

		if !m.Ok {
			return nil, errors.New("response is not ok")
		}

		pages = append(pages, m.Routes)

		if m.Next == "" {
			return pages, nil
		}

		params.Set("cursor", m.Next)
	}
}

type listTest struct {
	Params url.Values
	Pages  [][]*routeWithName
}

func TestAPIList(t *testing.T) {
	e := needEnv(t)
	defer e.destroy()

	rts := []*routeWithName{
		{
			Name: "0",
			Route: &context.Route{
				URL:  "http://0.com/",
				Time: time.Now(),
			},
		},

		{
			Name: "1",
			Route: &context.Route{
				URL:  "http://1.com/",
				Time: time.Now(),
			},
		},

		{
			Name: ":a",
			Route: &context.Route{
				URL:  "http://ga.com/",
				Time: time.Now(),
			},
		},

		{
			Name: ":b",
			Route: &context.Route{
				URL:  "http://gb.com/",
				Time: time.Now(),
			},
		},

		{
			Name: "a",
			Route: &context.Route{
				URL:  "http://a.com/",
				Time: time.Now(),
			},
		},

		{
			Name: "b",
			Route: &context.Route{
				URL:  "http://b.com/",
				Time: time.Now(),
			},
		},
	}

	for _, rt := range rts {
		if err := e.ctx.Put(rt.Name, rt.Route); err != nil {
			t.Fatal(err)
		}
	}

	tests := []*listTest{
		{
			Params: url.Values(map[string][]string{}),
			Pages: [][]*routeWithName{
				{rts[0], rts[1], rts[4], rts[5]},
			},
		},
		{
			Params: url.Values(map[string][]string{
				"include-generated-names": {"true"},
			}),
			Pages: [][]*routeWithName{rts},
		},
		{
			Params: url.Values(map[string][]string{
				"include-generated-names": {"false"},
			}),
			Pages: [][]*routeWithName{
				{rts[0], rts[1], rts[4], rts[5]},
			},
		},
		{
			Params: url.Values(map[string][]string{
				"limit": {"2"},
			}),
			Pages: [][]*routeWithName{
				{rts[0], rts[1]},
				{rts[4], rts[5]},
			},
		},
		{
			Params: url.Values(map[string][]string{
				"limit":                   {"2"},
				"include-generated-names": {"true"},
			}),
			Pages: [][]*routeWithName{
				{rts[0], rts[1]},
				{rts[2], rts[3]},
				{rts[4], rts[5]},
			},
		},
		{
			Params: url.Values(map[string][]string{
				"limit":  {"2"},
				"cursor": {base64.URLEncoding.EncodeToString([]byte{':'})},
			}),
			Pages: [][]*routeWithName{
				{rts[4], rts[5]},
			},
		},
		{
			Params: url.Values(map[string][]string{
				"limit":                   {"3"},
				"include-generated-names": {"true"},
				"cursor":                  {base64.URLEncoding.EncodeToString([]byte{':'})},
			}),
			Pages: [][]*routeWithName{
				{rts[2], rts[3], rts[4]},
				{rts[5]},
			},
		},
		{
			Params: url.Values(map[string][]string{
				"limit": {"1"},
			}),
			Pages: [][]*routeWithName{
				{rts[0]},
				{rts[1]},
				{rts[4]},
				{rts[5]},
			},
		},
		{
			Params: url.Values(map[string][]string{
				"cursor": {base64.URLEncoding.EncodeToString([]byte{'z'})},
			}),
			Pages: [][]*routeWithName{nil},
		},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("Test with ?%s", test.Params.Encode()),
			func(t *testing.T) {
				pages, err := getInPages(e, test.Params)
				if err != nil {
					t.Fatal(err)
				}

				if len(pages) != len(test.Pages) {
					t.Fatalf("number of pages mismatch %d vs %d", len(pages), len(test.Pages))
				}

				for i, n := 0, len(pages); i < n; i++ {
					page := pages[i]
					expected := test.Pages[i]

					if len(page) != len(expected) {
						t.Fatalf("page %d, length mismatch expected %d got %d", i, len(expected), len(page))
					}

					for j, m := 0, len(page); j < m; j++ {
						mustBeSameNamedRoute(t, page[j], expected[j])
					}
				}
			})
	}
}

func TestBadList(t *testing.T) {
	e := needEnv(t)
	defer e.destroy()

	tests := map[string]int{
		url.Values{
			"cursor": {"not a cursor"},
		}.Encode(): http.StatusBadRequest,

		url.Values{
			"limit": {"0"},
		}.Encode(): http.StatusBadRequest,

		url.Values{
			"limit": {"not a limit"},
		}.Encode(): http.StatusBadRequest,

		url.Values{
			"limit": {"100000"},
		}.Encode(): http.StatusBadRequest,

		url.Values{
			"include-generated-names": {"butter"},
		}.Encode(): http.StatusBadRequest,
	}

	for params, status := range tests {
		res, err := e.get("/api/urls/?" + params)
		if err != nil {
			t.Fatal(err)
		}

		mustHaveStatus(t, res, status)

		var m msgErr
		if err := json.NewDecoder(res).Decode(&m); err != nil {
			t.Fatal(err)
		}

		mustBeErr(t, &m)
	}
}
