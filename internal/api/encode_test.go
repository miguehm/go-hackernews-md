package api

import (
	"encoding/json"
	"testing"
)

func TestMarshalWithin(t *testing.T) {
	resp := struct {
		Items []string `json:"items"`
	}{Items: []string{"aaaa", "bbbb", "cccc", "dddd"}}
	full, _ := json.Marshal(resp)

	data, truncated, err := marshalWithin(len(full), &resp, func() bool { return dropLast(&resp.Items) })
	if err != nil || truncated || len(resp.Items) != 4 || string(data) != string(full) {
		t.Fatalf("fits: data=%s truncated=%v err=%v", data, truncated, err)
	}

	data, truncated, err = marshalWithin(len(`{"items":["aaaa","bbbb"]}`), &resp, func() bool { return dropLast(&resp.Items) })
	if err != nil || !truncated || string(data) != `{"items":["aaaa","bbbb"]}` {
		t.Fatalf("shrunk: data=%s truncated=%v err=%v", data, truncated, err)
	}

	data, truncated, err = marshalWithin(1, &resp, func() bool { return dropLast(&resp.Items) })
	if err != nil || !truncated || string(data) != `{"items":[]}` {
		t.Fatalf("cannot fit: data=%s truncated=%v err=%v", data, truncated, err)
	}

	if _, _, err := marshalWithin(10, func() {}, func() bool { return false }); err == nil {
		t.Fatal("want encode error")
	}
}
