package main

import (
	"embed"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	cbor "github.com/brianolson/cbor_go"
)

//go:embed static
var sfs embed.FS

var maxSize int64 = 10_000_000

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

type receiverServer struct {
	// write to outdir/{timestamp}
	outdir string

	// append to one big file instead of outdir/files
	appendfile string

	// access token
	secret string

	outPathTemplate string

	// write just the data, not cbor ReceiverRecord
	rawOut bool

	// only accept this "Content-Type"
	contentType string
}

type ReceiverRecord struct {
	When        int64  `json:"t"`
	Data        []byte `json:"d"`
	ContentType string `json:"Content-Type"`
}

func (rs *receiverServer) ServeHTTP(out http.ResponseWriter, request *http.Request) {
	var err error
	if rs.secret == "" {
		// ok
	} else if strings.Contains(request.URL.Path, rs.secret) {
		// ok
	} else if request.Header.Get("X-Receiver-Token") == rs.secret {
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
	if (rs.contentType != "") && (rs.contentType != request.Header.Get("Content-Type")) {
		http.Error(out, "unacceptable content-type", 400)
		return
	}
	reader := http.MaxBytesReader(out, request.Body, maxSize)
	data, err := io.ReadAll(reader)
	if err != nil {
		log.Printf("read body, %v", err)
		http.Error(out, err.Error(), 500)
		return
	}

	var blob []byte
	if rs.rawOut {
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
	if rs.appendfile != "" {
		if rs.appendfile == "-" {
			fout = os.Stdout
		} else {
			fout, err = os.OpenFile(rs.appendfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		}
		fpath = rs.appendfile
	} else {
		if rs.outPathTemplate != "" {
			fpath = formatTemplateString(rs.outPathTemplate, time.Now())
		} else {
			fname := time.Now().Format(timestampFormat)
			fpath = filepath.Join(rs.outdir, fname)
		}
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

func main() {
	var rs receiverServer
	serveAddr := flag.String("addr", ":8777", "Server Addr")
	flag.StringVar(&rs.secret, "secret", "", "access token")
	flag.StringVar(&rs.outdir, "outdir", ".", "directory to write files to")
	flag.StringVar(&rs.outPathTemplate, "out", "", "path template to write files to. %T gets timestamp")
	flag.StringVar(&rs.appendfile, "append", "", "append to one file instead of writing files")
	flag.Int64Var(&maxSize, "max", 10_000_000, "maximum object to receive")
	flag.BoolVar(&rs.rawOut, "raw", false, "write raw data instead of cbor ReceiverRecord")
	flag.StringVar(&rs.contentType, "content-type", "", "only accept this Content-Type:")
	flag.Parse()

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
