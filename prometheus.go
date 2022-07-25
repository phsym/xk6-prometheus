// MIT License
//
// Copyright (c) 2021 Iván Szkiba
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package prometheus

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"github.com/szkiba/xk6-prometheus/internal"

	"github.com/gorilla/schema"
	"go.k6.io/k6/output"
)

const defaultPort = 5656

// Register the extensions on module initialization.
func init() {
	output.RegisterExtension("prometheus", New)
}

type options struct {
	Port      int
	Host      string
	Subsystem string
	Namespace string
}

type Output struct {
	output.SampleBuffer
	adapter *internal.PrometheusAdapter

	addr    string
	arg     string
	logger  logrus.FieldLogger
	flusher *output.PeriodicFlusher
}

func New(params output.Params) (output.Output, error) {
	registry, ok := prometheus.DefaultRegisterer.(*prometheus.Registry)
	if !ok {
		registry = prometheus.NewRegistry()
	}

	o := &Output{
		adapter: internal.NewPrometheusAdapter(registry, params.Logger, "", ""),
		arg:     params.ConfigArgument,
		logger:  params.Logger,
	}

	return o, nil
}

func (o *Output) Description() string {
	return fmt.Sprintf("prometheus (%s)", o.addr)
}

func getopts(qs string) (*options, error) {
	opts := &options{
		Port:      defaultPort,
		Host:      "",
		Namespace: "",
		Subsystem: "",
	}

	if qs == "" {
		return opts, nil
	}

	v, err := url.ParseQuery(qs)
	if err != nil {
		return nil, err
	}

	decoder := schema.NewDecoder()

	if err = decoder.Decode(opts, v); err != nil {
		return nil, err
	}

	return opts, nil
}

func (o *Output) flush() {
	t := time.Now()
	buff := o.SampleBuffer.GetBufferedSamples()
	o.adapter.AddMetricSamples(buff)
	d := time.Since(t)
	if d > time.Second {
		o.logger.
			WithField("flush_duration", d.String()).
			WithField("sample_count", len(buff)).
			Warn("flush took more than 1s")
	}
}

func (o *Output) Start() error {
	opts, err := getopts(o.arg)
	if err != nil {
		return err
	}

	o.adapter.Namespace = opts.Namespace
	o.adapter.Subsystem = opts.Subsystem
	o.addr = fmt.Sprintf("%s:%d", opts.Host, opts.Port)

	listener, err := net.Listen("tcp", o.addr)
	if err != nil {
		return err
	}

	mux := &http.ServeMux{}
	mux.Handle("/", o.adapter.Handler())
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	go func() {
		// if err := http.Serve(listener, o.adapter.Handler()); err != nil {
		if err := http.Serve(listener, mux); err != nil {
			o.logger.Error(err)
		}
	}()

	flusher, err := output.NewPeriodicFlusher(time.Second, o.flush)
	if err != nil {
		return err
	}
	o.flusher = flusher

	return nil
}

func (o *Output) Stop() error {
	if o.flusher != nil {
		o.flusher.Stop()
		o.flusher = nil
	}
	return nil
}
