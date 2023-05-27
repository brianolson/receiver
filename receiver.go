package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	//	"path/filepath"
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

type ReceiverRecord struct {
	When        int64  `json:"t"`
	Data        []byte `json:"d"`
	ContentType string `json:"Content-Type"`
}

type receiverServer struct {
	configs map[string]ReceiverUnitConfig
}

func (rs *receiverServer) ServeHTTP(out http.ResponseWriter, request *http.Request) {
	request.ParseForm()
	configName := request.FormValue("d")
	cfg, some := rs.configs[configName]
	if !some {
		http.Error(out, "nope", http.StatusNotFound)
		return
	}
	var err error
	if cfg.Secret == "" {
		// ok
	} else if strings.Contains(request.URL.Path, cfg.Secret) {
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
		log.Printf("read body, %v", err)
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
			log.Printf("cbor d %v", err)
			http.Error(out, err.Error(), 500)
			return
		}
	}
	var fout io.WriteCloser
	var fpath string
	if cfg.AppendPath != "" {
		if cfg.AppendPath == "-" {
			fout = os.Stdout
		} else {
			fout, err = os.OpenFile(cfg.AppendPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		}
		fpath = cfg.AppendPath
	} else {
		fpath = formatTemplateString(cfg.OutTemplate, time.Now())
		fout, err = os.Create(fpath)
		defer fout.Close()
	}
	if err != nil {
		log.Printf("%s: open %v", fpath, err)
		http.Error(out, err.Error(), 500)
		return
	}
	_, err = fout.Write(blob)
	if err != nil {
		log.Printf("%s: write %v", fpath, err)
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
	AppendPath string `json:"append"`

	// ContentType must match HTTP POST header Content-Type
	ContentType string `json:"Content-Type"`

	MaxSize int64 `json:"max_ob_bytes"`
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
	var defaultReceiver ReceiverUnitConfig
	serveAddr := flag.String("addr", ":8777", "Server Addr")
	flag.StringVar(&defaultReceiver.Secret, "secret", "", "access token")
	flag.StringVar(&defaultReceiver.OutTemplate, "out", "", "path template to write files to. %T gets timestamp")
	flag.StringVar(&defaultReceiver.AppendPath, "append", "", "append to one file instead of writing files")
	flag.Int64Var(&defaultReceiver.MaxSize, "max", 10_000_000, "maximum object to receive")
	flag.BoolVar(&defaultReceiver.Raw, "raw", false, "write raw data instead of cbor ReceiverRecord")
	flag.StringVar(&defaultReceiver.ContentType, "content-type", "", "only accept this Content-Type:")

	var configPath string
	flag.StringVar(&configPath, "cfg", "", "json config file")
	flag.Parse()

	if configPath != "" {
		fin, err := os.Open(configPath)
		maybefail(err, "%s: %s", configPath, err)
		dec := json.NewDecoder(fin)
		err = dec.Decode(&rs.configs)
		maybefail(err, "%s: bad json, %s", configPath, err)
	} else {
		rs.configs = make(map[string]ReceiverUnitConfig, 1)
	}
	if defaultReceiver.OutTemplate != "" || defaultReceiver.AppendPath != "" {
		rs.configs[""] = defaultReceiver
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
	log.Print("serving on ", *serveAddr)
	log.Fatal(server.ListenAndServe())
}
