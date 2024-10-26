package data

type ReceiverRecord struct {
	When        int64  `json:"t"`
	Data        []byte `json:"d"`
	ContentType string `json:"Content-Type"`
}
