package weixin

import (
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	cloudaudio "orrery_v2/internal/agent/runtime/audio"
)

const maxIlinkImageBytes = 16 << 20

func (c *IlinkBotClient) resolveInboundAttachments(ctx context.Context, message InboundMessage) (InboundMessage, error) {
	if len(message.Attachments) == 0 {
		return message, nil
	}
	opts, client, _ := c.snapshot()
	if client == nil {
		return InboundMessage{}, errors.New("weixin ilink media client is not connected")
	}
	resolved := append([]InboundAttachment(nil), message.Attachments...)
	totalBytes := 0
	for index := range resolved {
		attachment := &resolved[index]
		isAudio := strings.EqualFold(strings.TrimSpace(attachment.Type), "audio")
		if !strings.EqualFold(strings.TrimSpace(attachment.Type), "image") && !isAudio {
			continue
		}
		data, mediaType, err := downloadIlinkMedia(ctx, client, opts, *attachment, c.allowInsecureLoopback)
		if err != nil {
			return InboundMessage{}, fmt.Errorf("download Weixin image %d: %w", index+1, err)
		}
		totalBytes += len(data)
		if totalBytes > maxIlinkImageBytes {
			return InboundMessage{}, errors.New("combined Weixin images exceed the 16 MiB limit")
		}
		attachment.Name = fmt.Sprintf("weixin-image-%d%s", index+1, imageExtension(mediaType))
		if isAudio {
			attachment.Name = fmt.Sprintf("weixin-voice-%d", index+1)
		}
		attachment.MIMEType = mediaType
		attachment.Bytes = int64(len(data))
		attachment.ContentBase64 = base64.StdEncoding.EncodeToString(data)
		attachment.FullURL = ""
		attachment.EncryptQueryParam = ""
		attachment.AESKey = ""
		attachment.AESKeyHex = ""
	}
	message.Attachments = resolved
	return message, nil
}

func downloadIlinkImage(ctx context.Context, client *http.Client, opts Options, attachment InboundAttachment, allowInsecureLoopback bool) ([]byte, string, error) {
	attachment.Type = "image"
	return downloadIlinkMedia(ctx, client, opts, attachment, allowInsecureLoopback)
}

func downloadIlinkMedia(ctx context.Context, client *http.Client, opts Options, attachment InboundAttachment, allowInsecureLoopback bool) ([]byte, string, error) {
	downloadURL, err := ilinkImageDownloadURL(opts, attachment, allowInsecureLoopback)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", errors.New("create Weixin image request")
	}
	downloadClient := *client
	downloadClient.Jar = nil
	downloadClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := downloadClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", errors.New("fetch Weixin media failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, "", fmt.Errorf("Weixin image CDN returned HTTP %d", resp.StatusCode)
	}
	maxDownloadBytes := int64(maxIlinkImageBytes + aes.BlockSize)
	if resp.ContentLength > maxDownloadBytes {
		return nil, "", errors.New("Weixin image exceeds the 16 MiB limit")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read Weixin image: %w", err)
	}
	if int64(len(raw)) > maxDownloadBytes {
		return nil, "", errors.New("Weixin image exceeds the 16 MiB limit")
	}
	key, encrypted, err := ilinkImageAESKey(attachment)
	if err != nil {
		return nil, "", err
	}
	data := raw
	if encrypted {
		data, err = decryptIlinkImage(raw, key)
		if err != nil {
			return nil, "", err
		}
	}
	if len(data) == 0 || len(data) > maxIlinkImageBytes {
		return nil, "", errors.New("Weixin image is empty or exceeds the 16 MiB limit")
	}
	if strings.EqualFold(attachment.Type, "audio") {
		mediaType := cloudaudio.DetectMIMEType(data)
		if cloudaudio.IsSILK(data) {
			mediaType = "audio/silk"
		}
		return data, mediaType, nil
	}
	mediaType := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
	if !supportedIlinkImageType(mediaType) {
		return nil, "", fmt.Errorf("unsupported Weixin image media type %q", mediaType)
	}
	return data, mediaType, nil
}

func ilinkImageDownloadURL(opts Options, attachment InboundAttachment, allowInsecureLoopback bool) (string, error) {
	var fullURLError error
	if fullURL := strings.TrimSpace(attachment.FullURL); fullURL != "" {
		if err := validateIlinkMediaURL(fullURL, allowInsecureLoopback); err == nil {
			return fullURL, nil
		} else {
			fullURLError = err
		}
	}
	queryParam := strings.TrimSpace(attachment.EncryptQueryParam)
	if queryParam == "" {
		if fullURLError != nil {
			return "", fullURLError
		}
		return "", errors.New("Weixin image has no CDN download reference")
	}
	baseURL := strings.TrimSpace(opts.IlinkCDNBaseURL)
	if err := validateIlinkMediaURL(baseURL, allowInsecureLoopback); err != nil {
		return "", err
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.New("invalid Weixin image CDN base URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/download"
	query := parsed.Query()
	query.Set("encrypted_query_param", queryParam)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func validateIlinkMediaURL(rawURL string, allowInsecureLoopback bool) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("invalid Weixin image CDN URL")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if allowInsecureLoopback && isLoopbackIlinkHost(host) && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		return nil
	}
	if parsed.Scheme != "https" || (parsed.Port() != "" && parsed.Port() != "443") {
		return errors.New("Weixin image CDN URL must use standard HTTPS")
	}
	if host != "weixin.qq.com" && !strings.HasSuffix(host, ".weixin.qq.com") {
		return errors.New("Weixin image CDN URL must use an official weixin.qq.com host")
	}
	return nil
}

func ilinkImageAESKey(attachment InboundAttachment) ([]byte, bool, error) {
	if value := strings.TrimSpace(attachment.AESKeyHex); value != "" {
		key, err := hex.DecodeString(value)
		if err != nil || len(key) != aes.BlockSize {
			return nil, false, errors.New("invalid Weixin image AES key")
		}
		return key, true, nil
	}
	value := strings.TrimSpace(attachment.AESKey)
	if value == "" {
		return nil, false, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return nil, false, errors.New("invalid Weixin image AES key")
	}
	if len(decoded) == aes.BlockSize {
		return decoded, true, nil
	}
	if len(decoded) == 2*aes.BlockSize {
		key, err := hex.DecodeString(string(decoded))
		if err == nil && len(key) == aes.BlockSize {
			return key, true, nil
		}
	}
	return nil, false, errors.New("invalid Weixin image AES key")
}

func decryptIlinkImage(ciphertext, key []byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("invalid encrypted Weixin image length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid Weixin image AES key")
	}
	plaintext := make([]byte, len(ciphertext))
	for offset := 0; offset < len(ciphertext); offset += aes.BlockSize {
		block.Decrypt(plaintext[offset:offset+aes.BlockSize], ciphertext[offset:offset+aes.BlockSize])
	}
	padding := int(plaintext[len(plaintext)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plaintext) {
		return nil, errors.New("invalid encrypted Weixin image padding")
	}
	for _, value := range plaintext[len(plaintext)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid encrypted Weixin image padding")
		}
	}
	return plaintext[:len(plaintext)-padding], nil
}

func supportedIlinkImageType(mediaType string) bool {
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp":
		return true
	default:
		return false
	}
}

func imageExtension(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	default:
		return ".bin"
	}
}
