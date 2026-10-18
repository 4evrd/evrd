// Package metricsq reads evrd's own metrics back from VictoriaMetrics (or any
// Prometheus-compatible query API) for the dashboards: a user's traffic per
// network, reconnects and time offline. evrd writes them with OpenTelemetry
// (internal/telemetry); this is the read side.
package metricsq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrUnavailable means no metrics store is configured.
var ErrUnavailable = errors.New("metrics: no metrics store configured")

// Client queries a Prometheus-compatible API at a base URL such as
// http://victoriametrics:8428.
type Client struct {
	base string
	hc   *http.Client
}

// New returns a client, or nil when base is empty (metrics off).
func New(base string) *Client {
	if base == "" {
		return nil
	}
	return &Client{base: strings.TrimRight(base, "/"), hc: &http.Client{Timeout: 10 * time.Second}}
}

// Series is one labelled series of [unix seconds, value] points.
type Series struct {
	Labels map[string]string
	Points [][2]float64
}

type apiResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
			Values [][2]any          `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (*apiResponse, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	defer res.Body.Close()
	var out apiResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("metrics: %s: %w", res.Status, err)
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("metrics: %s", out.Error)
	}
	return &out, nil
}

func num(v any) float64 {
	s, _ := v.(string)
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// Range runs a range query.
func (c *Client) Range(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Series, error) {
	out, err := c.get(ctx, "/api/v1/query_range", url.Values{
		"query": {query},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.FormatInt(int64(step.Seconds()), 10)},
	})
	if err != nil {
		return nil, err
	}
	series := make([]Series, 0, len(out.Data.Result))
	for _, r := range out.Data.Result {
		s := Series{Labels: r.Metric}
		for _, p := range r.Values {
			t, _ := p[0].(float64)
			s.Points = append(s.Points, [2]float64{t, num(p[1])})
		}
		series = append(series, s)
	}
	sort.Slice(series, func(i, j int) bool { return fmt.Sprint(series[i].Labels) < fmt.Sprint(series[j].Labels) })
	return series, nil
}

// Scalar runs an instant query expected to return one number (0 when empty).
func (c *Client) Scalar(ctx context.Context, query string) (float64, error) {
	out, err := c.get(ctx, "/api/v1/query", url.Values{"query": {query}})
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, r := range out.Data.Result {
		total += num(r.Value[1])
	}
	return total, nil
}

// Quote makes a string safe inside a PromQL label matcher.
func Quote(s string) string { return strconv.Quote(s) }
