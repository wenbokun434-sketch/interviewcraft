package runner

import "testing"

func TestLimitedBufferNeverExceedsConfiguredOutput(t *testing.T) {
	buffer := &limitedBuffer{limit: 4}
	written, err := buffer.Write([]byte("123456"))
	if err != nil || written != 6 || string(buffer.Bytes()) != "1234" {
		t.Fatalf("Write=(%d,%v) value=%q", written, err, buffer.Bytes())
	}
	copy := buffer.Bytes()
	copy[0] = 'x'
	if string(buffer.Bytes()) != "1234" {
		t.Fatal("Bytes exposed mutable storage")
	}
}
