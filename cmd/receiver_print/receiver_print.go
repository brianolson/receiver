package main

import (
	"bolson.org/receiver/data"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	cbor "github.com/brianolson/cbor_go"
	"io"
	"os"
	"strings"
)

type PrintableReceiverRecord struct {
	When        int64  `json:"t"`
	Data        string `json:"d"`
	ContentType string `json:"Content-Type"`
}

type JSONReceiverRecord struct {
	When        int64          `json:"t"`
	Data        map[string]any `json:"d"`
	ContentType string         `json:"Content-Type"`
}

func isPrintableContentType(contentType string) bool {
	if strings.HasPrefix(contentType, "application/json") {
		return true
	}
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	return false
}

func prettyPrintJson(fin io.Reader, out io.Writer) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	dec := cbor.NewDecoder(fin)
	var rec data.ReceiverRecord
	for {
		err := dec.Decode(&rec)
		if err != nil {
			return err
		}
		if strings.HasPrefix(rec.ContentType, "text/") {
			prec := PrintableReceiverRecord{
				When:        rec.When,
				Data:        string(rec.Data),
				ContentType: rec.ContentType,
			}
			err = enc.Encode(prec)
			if err != nil {
				return err
			}
		} else if strings.HasPrefix(rec.ContentType, "application/json") {
			jrec := JSONReceiverRecord{
				When:        rec.When,
				ContentType: rec.ContentType,
			}
			jrec.Data = make(map[string]any)
			err = json.Unmarshal(rec.Data, &jrec.Data)
			if err != nil {
				return fmt.Errorf("sub unmarshal, %w", err)
			}
			err = enc.Encode(jrec)
			if err != nil {
				return err
			}
		} else {
			err = enc.Encode(&rec)
			if err != nil {
				return err
			}
		}
		_, err = out.Write([]byte("\n"))
		if err != nil {
			return err
		}
	}
	return nil
}
func jsonPerLine(fin io.Reader, out io.Writer) error {
	enc := json.NewEncoder(out)
	dec := cbor.NewDecoder(fin)
	var rec data.ReceiverRecord
	for {
		err := dec.Decode(&rec)
		if err != nil {
			return err
		}
		if isPrintableContentType(rec.ContentType) {
			prec := PrintableReceiverRecord{
				When:        rec.When,
				Data:        string(rec.Data),
				ContentType: rec.ContentType,
			}
			err = enc.Encode(prec)
			if err != nil {
				return err
			}
		} else {
			err = enc.Encode(&rec)
			if err != nil {
				return err
			}
		}
		// Where is the newline coming from?
		//_, err = out.Write([]byte("\n"))
		//if err != nil {
		//	return err
		//}
	}
}

func main() {
	var pretty bool
	flag.BoolVar(&pretty, "pretty", false, "Pretty print JSON")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		if pretty {
			prettyPrintJson(os.Stdin, os.Stdout)
		} else {
			jsonPerLine(os.Stdin, os.Stdout)
		}
	} else {
		for _, path := range args {
			fin, err := os.Open(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, "%s: %s\n", path, err)
			}
			if pretty {
				err = prettyPrintJson(fin, os.Stdout)
			} else {
				err = jsonPerLine(fin, os.Stdout)
			}
			if errors.Is(err, io.EOF) {
				// okay!
			} else if err != nil {
				fmt.Fprintln(os.Stderr, "%s: %s\n", path, err)
			}
		}
	}
}
