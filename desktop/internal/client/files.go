package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"finesub-review-desktop/internal/localstore"
)

type UploadFile struct {
	Path string
	Name string
}

// Upload builds a private temporary multipart file so requests have the exact
// Content-Length required by the service, without loading all files into RAM.
func (c *Client) Upload(ctx context.Context, path string, files []UploadFile, fields map[string]string, out any) error {
	if len(files) == 0 || len(files) > 200 { return errors.New("请选择 1 至 200 个上传文件") }
	if _, err := c.apiURL(path); err != nil { return err }
	temp, err := os.CreateTemp("", "finesub-upload-*")
	if err != nil { return err }
	defer os.Remove(temp.Name())
	defer temp.Close()
	writer := multipart.NewWriter(temp)
	keys := make([]string,0,len(fields))
	for key := range fields { keys = append(keys,key) }
	sort.Strings(keys)
	for _, key := range keys {
		if err := writer.WriteField(key, fields[key]); err != nil { return err }
	}
	const maxTotal int64 = 30 << 20
	var total int64
	for _, item := range files {
		if err := ctx.Err(); err != nil { return err }
		name := strings.ReplaceAll(item.Name, "\\", "/")
		if name == "" { name = filepath.Base(item.Path) }
		if err := validateUploadName(name); err != nil { return err }
		file, err := os.Open(item.Path)
		if err != nil { return fmt.Errorf("读取上传文件 %s: %w", name, err) }
		info, err := file.Stat()
		if err != nil { file.Close(); return err }
		if !info.Mode().IsRegular() { file.Close(); return errors.New("只能上传普通文件") }
		part, err := writer.CreateFormFile("files", name)
		if err != nil { file.Close(); return err }
		n, copyErr := io.Copy(part, io.LimitReader(file, maxTotal-total+1))
		file.Close()
		if copyErr != nil { return copyErr }
		total += n
		if total > maxTotal { return errors.New("单批上传总量不能超过 30 MB") }
	}
	if err := writer.Close(); err != nil { return err }
	info, err := temp.Stat()
	if err != nil { return err }
	if info.Size() > 32<<20 { return errors.New("上传请求不能超过 32 MB") }
	if _, err := temp.Seek(0, io.SeekStart); err != nil { return err }
	response, err := c.request(ctx,http.MethodPost,path,temp,writer.FormDataContentType(),info.Size())
	if err != nil { return err }
	defer response.Body.Close()
	return unconfirmed(http.MethodPost, c.decode(response,path,out))
}

func validateUploadName(name string) error {
	if strings.HasPrefix(name,"/") || strings.ContainsAny(name, ":\x00\r\n") { return errors.New("上传文件名必须是相对路径") }
	for _, part := range strings.Split(name,"/") {
		if part == "" || part == "." || part == ".." { return errors.New("上传文件路径无效") }
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip", ".md", ".jsonl": return nil
	default: return errors.New("仅接受 ZIP、Markdown 和 JSONL 文件")
	}
}

// Download streams into a temporary sibling of destination, then atomically
// replaces it. Failed, truncated or cancelled downloads preserve the old file.
func (c *Client) Download(ctx context.Context, path, destination string) error {
	if destination == "" { return errors.New("请选择文件保存位置") }
	response, err := c.request(ctx,http.MethodGet,path,nil,"",0)
	if err != nil { return err }
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 { return responseError(response) }
	if response.StatusCode != http.StatusOK { return errors.New("服务器未返回完整下载文件") }
	temp, err := os.CreateTemp(filepath.Dir(destination), ".finesub-download-*")
	if err != nil { return fmt.Errorf("创建下载文件: %w",err) }
	defer os.Remove(temp.Name())
	defer temp.Close()
	n, err := io.Copy(temp,response.Body)
	if err != nil { return fmt.Errorf("下载中断，原文件保持不变: %w",err) }
	if response.ContentLength >= 0 && n != response.ContentLength { return errors.New("下载内容不完整，原文件保持不变") }
	if err := ctx.Err(); err != nil { return err }
	if err := temp.Sync(); err != nil { return err }
	if err := temp.Close(); err != nil { return err }
	if err := localstore.ReplaceFile(temp.Name(),destination); err != nil { return fmt.Errorf("保存下载文件: %w",err) }
	return nil
}
