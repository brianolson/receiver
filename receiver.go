package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	cbor "github.com/brianolson/cbor_go"
)

//go:embed static
var sfs embed.FS

const timestampFormat = "20060102_150405.999999999"

func formatTemplateString(x string, when time.Time) string {
	// "%%" becomes "%"
	// e.g. "%%T" -> "%T"
	parts := strings.Split(x, "%%")
	timestamp := when.Format(timestampFormat)
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(p, "%T", timestamp)
	}
	return strings.Join(parts, "%")
}

func formatAppendTemplateString(x string, unixSeconds int64) string {
	// "%%" becomes "%"
	// e.g. "%%T" -> "%T"
	parts := strings.Split(x, "%%")
	timestamp := strconv.FormatInt(unixSeconds, 10)
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(p, "%T", timestamp)
	}
	return strings.Join(parts, "%")
}

type ReceiverRecord struct {
	When        int64  `json:"t"`
	Data        []byte `json:"d"`
	ContentType string `json:"Content-Type"`
}

type ReceiverUnit struct {
	ReceiverUnitConfig

	fpath string
	fout  io.WriteCloser
}

type receiverServer struct {
	configs map[string]*ReceiverUnit
}

// Many ways to do it
// ?d=configuration_name
// /whatever/{configuration_name}/{secret}
// Authorization: whatever {secret}
// X-Receiver-Token: {secret}
func (rs *receiverServer) ServeHTTP(out http.ResponseWriter, request *http.Request) {
	request.ParseForm()
	pathParts := strings.Split(request.URL.Path, "/")
	configName := request.FormValue("d")
	cfg, some := rs.configs[configName]
	if !some {
		for _, part := range pathParts {
			cfg, some = rs.configs[part]
			if some {
				break
			}
		}
		if !some {
			http.Error(out, "nope", http.StatusNotFound)
			return
		}
	}
	var err error
	foundSecret := false
	for _, part := range pathParts {
		if cfg.Secret != "" && cfg.Secret == part {
			foundSecret = true
		}
	}
	if cfg.Secret == "" {
		// ok
	} else if foundSecret {
		// ok
	} else if strings.Contains(request.Header.Get("Authorization"), cfg.Secret) {
		// ok
	} else if request.Header.Get("X-Receiver-Token") == cfg.Secret {
		// ok
	} else {
		http.Error(out, "nope", http.StatusForbidden)
		return
	}
	out.Header()["Content-Type"] = []string{"text/plain"}
	if request.Method != "POST" {
		http.Error(out, "not POST", 400)
		return
	}
	if (cfg.ContentType != "") && (cfg.ContentType != request.Header.Get("Content-Type")) {
		http.Error(out, "unacceptable content-type", 400)
		return
	}
	reader := http.MaxBytesReader(out, request.Body, cfg.MaxSize)
	data, err := io.ReadAll(reader)
	if err != nil {
		slog.Debug("read body", "err", err)
		http.Error(out, err.Error(), 500)
		return
	}

	var blob []byte
	if cfg.Raw {
		blob = data
	} else {
		var rec ReceiverRecord
		rec.When = time.Now().UnixMilli()
		rec.Data = data
		rec.ContentType = request.Header.Get("Content-Type")
		blob, err = cbor.Dumps(rec)
		if err != nil {
			slog.Debug("cbor d", "err", err)
			http.Error(out, err.Error(), 500)
			return
		}
	}
	var fout io.WriteCloser
	var fpath string
	if cfg.AppendPath != "" {
		if cfg.AppendPath == "-" {
			fout = os.Stdout
			fpath = cfg.AppendPath
		} else {
			nfpath := cfg.GenerateAppendPath(time.Now())
			if nfpath == cfg.fpath {
				fout = cfg.fout
			} else {
				if cfg.fout != nil {
					cfg.fout.Close()
					cfg.fout = nil
				}
				fout, err = os.OpenFile(nfpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				cfg.fout = fout
				cfg.fpath = nfpath
			}
			fpath = cfg.fpath
		}
	} else {
		fpath = formatTemplateString(cfg.OutTemplate, time.Now())
		fout, err = os.Create(fpath)
		defer fout.Close()
	}
	if err != nil {
		slog.Debug("open", "path", fpath, "err", err)
		http.Error(out, err.Error(), 500)
		return
	}
	_, err = fout.Write(blob)
	if err != nil {
		slog.Debug("write", "path", fpath, "err", err)
		http.Error(out, err.Error(), 500)
		return
	}
}

func faviconHandler(out http.ResponseWriter, request *http.Request) {
	faviconBytes, err := sfs.ReadFile("static/favicon.ico")
	if err != nil {
		http.NotFound(out, request)
		return
	}
	out.Header()["Content-Type"] = []string{"image/vnd.microsoft.icon"}
	out.WriteHeader(http.StatusOK)
	out.Write(faviconBytes)
}

// ReceiverUnitConfig
// e.g. {"raw": true, "secret": "hunter2", "out": "/wat/%T.jpg", "Content-Type": "image/jpeg"}
type ReceiverUnitConfig struct {
	// Raw write POST body out raw to a file
	// Default writes a CBOR ReceiverRecord
	Raw bool `json:"raw"`

	// POST request must include this secret
	Secret string `json:"secret"`

	// OutTemplate forms output file path
	// "%%" becomes "%"
	// e.g. "%%T" -> "%T"
	OutTemplate string `json:"out"`

	// AppendPath receives CBOR ReceiverRecord
	// AppendPath %T gets unix seconds base 10
	// AppendPath %T unix seconds are clamped to modulo and offset from AppendMod and AppendOffset
	// ```
	// nowu := now.Unix()
	// nowu = nowu - ((nowu + ruc.AppendOffset) % ruc.AppendMod)
	// ```
	AppendPath string `json:"append"`

	// AppendMod if non-zero changes %T in AppendPath
	AppendMod int64 `json:"append-mod"`

	AppendOffset int64 `json:"append-offset"`

	// ContentType must match HTTP POST header Content-Type
	ContentType string `json:"Content-Type"`

	MaxSize int64 `json:"max_ob_bytes"`
}

func (ruc *ReceiverUnitConfig) GenerateAppendPath(now time.Time) string {
	nowu := now.Unix()
	if ruc.AppendMod == 0 {
		return formatAppendTemplateString(ruc.AppendPath, nowu)
	}
	remainder := (nowu + ruc.AppendOffset) % ruc.AppendMod
	nowu = nowu - remainder
	return formatAppendTemplateString(ruc.AppendPath, nowu)
}

func (ruc *ReceiverUnitConfig) sane() error {
	if ruc.Raw {
		if ruc.OutTemplate == "" {
			return errors.New("raw mode requires output template")
		}
	}
	if ruc.Secret == "" {
		return errors.New("secret must be set")
	}
	if ruc.OutTemplate == "" && ruc.AppendPath == "" {
		return errors.New("at least one of output template and append path must be set")
	}
	if ruc.MaxSize == 0 {
		ruc.MaxSize = 10_000_00
	}
	return nil
}

func maybefail(err error, msg string, p ...interface{}) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, msg, p...)
	os.Exit(1)
}

func main() {
	var rs receiverServer
	var defaultReceiver ReceiverUnit
	var verbose bool
	serveAddr := flag.String("addr", ":8777", "Server Addr")
	flag.StringVar(&defaultReceiver.Secret, "secret", "", "access token")
	flag.StringVar(&defaultReceiver.OutTemplate, "out", "", "path template to write files to. %T gets timestamp")
	flag.StringVar(&defaultReceiver.AppendPath, "append", "", "append to one file instead of writing files")
	flag.Int64Var(&defaultReceiver.MaxSize, "max", 10_000_000, "maximum object to receive")
	flag.BoolVar(&defaultReceiver.Raw, "raw", false, "write raw data instead of cbor ReceiverRecord")
	flag.StringVar(&defaultReceiver.ContentType, "content-type", "", "only accept this Content-Type:")
	flag.BoolVar(&verbose, "verbose", false, "verbose logging")

	var configPath string
	flag.StringVar(&configPath, "cfg", "", "json config file")
	flag.Parse()

	if verbose {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	}

	if configPath != "" {
		fin, err := os.Open(configPath)
		maybefail(err, "%s: %s", configPath, err)
		dec := json.NewDecoder(fin)
		err = dec.Decode(&rs.configs)
		maybefail(err, "%s: bad json, %s", configPath, err)
		slog.Debug("loaded config", "cfg", rs.configs)
	} else {
		rs.configs = make(map[string]*ReceiverUnit, 1)
	}
	if defaultReceiver.OutTemplate != "" || defaultReceiver.AppendPath != "" {
		rs.configs[""] = &defaultReceiver
	}
	for name, cfg := range rs.configs {
		err := cfg.sane()
		maybefail(err, "config[%#v]: %s", name, err)
		// write back any config cleanup
		rs.configs[name] = cfg
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/favicon.ico", faviconHandler)
	mux.Handle("/", &rs)

	server := &http.Server{
		Addr:    *serveAddr,
		Handler: mux,
	}
	slog.Info("serving on", "addr", *serveAddr)
	slog.Info("exiting", "err", server.ListenAndServe())
}
