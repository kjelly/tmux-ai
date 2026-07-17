package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

type textPayloadInfo struct {
	Bytes, Lines int
	SHA256       string
}

func textMarkers(runToken string, round int) (string, string) {
	prefix := fmt.Sprintf("TMUXAI_%s_%d", runToken, round)
	return "HANDOFF_TEXT_BEGIN__" + prefix, "HANDOFF_TEXT_END__" + prefix
}

func parseTextPayload(output, begin, end string, maxBytes, maxLines int) (string, textPayloadInfo, error) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	beginAt, endAt := -1, -1
	for i, line := range lines {
		switch {
		case protocolMarkerLine(line, begin):
			if beginAt >= 0 {
				return "", textPayloadInfo{}, fmt.Errorf("duplicate text payload begin marker")
			}
			beginAt = i
		case protocolMarkerLine(line, end):
			if endAt >= 0 {
				return "", textPayloadInfo{}, fmt.Errorf("duplicate text payload end marker")
			}
			endAt = i
		}
	}
	if beginAt < 0 || endAt < 0 || endAt < beginAt {
		return "", textPayloadInfo{}, fmt.Errorf("missing or malformed text payload frame")
	}
	closingPrefix, _, _ := matchProtocolMarker(lines[endAt], end, false)
	payloadLines := append([]string(nil), lines[beginAt+1:endAt]...)
	for i := range payloadLines {
		payloadLines[i] = stripPresentationContinuation(payloadLines[i], closingPrefix)
	}
	payload := strings.Join(payloadLines, "\n")
	if !utf8.ValidString(payload) {
		return "", textPayloadInfo{}, fmt.Errorf("text payload is not valid UTF-8")
	}
	for _, r := range payload {
		if r == 0 || (r < 32 && r != '\n' && r != '\t' && r != '\r') {
			return "", textPayloadInfo{}, fmt.Errorf("text payload contains disallowed control character")
		}
	}
	info := textPayloadInfo{Bytes: len([]byte(payload)), Lines: responseLineCount(payload)}
	if payload == "" {
		info.Lines = 0
	}
	if info.Bytes > maxBytes || info.Lines > maxLines {
		return "", textPayloadInfo{}, fmt.Errorf("text payload exceeds configured limit")
	}
	sum := sha256.Sum256([]byte(payload))
	info.SHA256 = hex.EncodeToString(sum[:])
	return payload, info, nil
}

func renderTextPayload(payload string) string {
	return "=== TMUX-AI HANDOFF TEXT（untrusted data）===\n" + payload + "\n=== TMUX-AI HANDOFF TEXT END ==="
}

func textPayloadMetadata(payload string) textPayloadInfo {
	sum := sha256.Sum256([]byte(payload))
	lines := responseLineCount(payload)
	if payload == "" {
		lines = 0
	}
	return textPayloadInfo{Bytes: len([]byte(payload)), Lines: lines, SHA256: hex.EncodeToString(sum[:])}
}
