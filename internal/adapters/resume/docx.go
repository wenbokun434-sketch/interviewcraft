package resume

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

func extractDOCX(payload []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return "", err
	}
	var document *zip.File
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return "", errors.New("DOCX is missing word/document.xml")
	}
	reader, err := document.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()

	decoder := xml.NewDecoder(io.LimitReader(reader, MaxFileBytes+1))
	var result strings.Builder
	inText := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch current := token.(type) {
		case xml.StartElement:
			switch current.Name.Local {
			case "t":
				inText = true
			case "tab":
				result.WriteByte('\t')
			case "br":
				result.WriteByte('\n')
			}
		case xml.EndElement:
			switch current.Name.Local {
			case "t":
				inText = false
			case "p":
				result.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				result.Write([]byte(current))
			}
		}
		if result.Len() > int(MaxFileBytes) {
			return "", errors.New("extracted DOCX text exceeds 10MB")
		}
	}
	return result.String(), nil
}
