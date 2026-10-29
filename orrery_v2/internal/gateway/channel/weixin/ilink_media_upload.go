package weixin

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5" // Required checksum in the Weixin upload protocol, not authentication.
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"orrery_v2/internal/gateway/channel"
)

func (c *IlinkBotClient) uploadOutboundImage(ctx context.Context, client *http.Client, opts Options, uin, to string, data []byte) (map[string]any, error) {
	if len(data) == 0 || len(data) > maxIlinkImageBytes || http.DetectContentType(data) != "image/png" {
		return nil, channel.PermanentDeliveryError(errors.New("invalid outbound Weixin image"))
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	key := make([]byte, aes.BlockSize)
	fileKey := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if _, err := rand.Read(fileKey); err != nil {
		return nil, err
	}
	fileID := hex.EncodeToString(fileKey)
	keyHex := hex.EncodeToString(key)
	digest := md5.Sum(data)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(data)%aes.BlockSize
	padded := append(append([]byte(nil), data...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	ciphertext := make([]byte, len(padded))
	// Weixin's CDN protocol requires AES-128-ECB with PKCS7 padding.
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		block.Encrypt(ciphertext[offset:offset+aes.BlockSize], padded[offset:offset+aes.BlockSize])
	}
	var upload struct {
		ReturnCode *int   `json:"ret"`
		ErrCode    *int   `json:"errcode"`
		FullURL    string `json:"upload_full_url"`
		Param      string `json:"upload_param"`
	}
	body := map[string]any{"filekey": fileID, "media_type": 1, "to_user_id": to, "rawsize": len(data), "rawfilemd5": hex.EncodeToString(digest[:]), "filesize": len(ciphertext), "no_need_thumb": true, "aeskey": keyHex, "base_info": c.baseInfo(opts)}
	if err := c.postJSON(ctx, client, opts, uin, "ilink/bot/getuploadurl", body, &upload); err != nil {
		return nil, errors.New("Weixin image upload authorization failed")
	}
	if (upload.ReturnCode != nil && *upload.ReturnCode != 0) || (upload.ErrCode != nil && *upload.ErrCode != 0) {
		return nil, errors.New("Weixin image upload authorization rejected")
	}
	uploadURL := strings.TrimSpace(upload.FullURL)
	if uploadURL == "" && upload.Param != "" {
		base, err := url.Parse(opts.IlinkCDNBaseURL)
		if err != nil {
			return nil, errors.New("invalid Weixin CDN URL")
		}
		base.Path = strings.TrimRight(base.Path, "/") + "/upload"
		q := base.Query()
		q.Set("encrypted_query_param", upload.Param)
		q.Set("filekey", fileID)
		base.RawQuery = q.Encode()
		uploadURL = base.String()
	}
	if err := validateIlinkMediaURL(uploadURL, c.allowInsecureLoopback); err != nil {
		return nil, channel.PermanentDeliveryError(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(ciphertext))
	if err != nil {
		return nil, errors.New("invalid Weixin upload request")
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	cdnClient := *client
	cdnClient.Jar = nil
	cdnClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := cdnClient.Do(req)
	if err != nil {
		return nil, errors.New("Weixin image CDN upload failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Weixin image CDN upload HTTP %d", resp.StatusCode)
	}
	param := resp.Header.Get("x-encrypted-param")
	if param == "" || len(param) > 16384 {
		return nil, errors.New("Weixin image CDN missing download reference")
	}
	return map[string]any{"type": 2, "image_item": map[string]any{"media": map[string]any{"encrypt_query_param": param, "aes_key": base64.StdEncoding.EncodeToString([]byte(keyHex)), "encrypt_type": 1}, "mid_size": len(ciphertext)}}, nil
}
