package localstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDraftSurvivesReopenWithoutChangingOriginalText(t *testing.T) {
	dir:=t.TempDir()
	s,err:=Open(dir);if err!=nil {t.Fatal(err)}
	key:="https://example.com:user:123:entry:456"
	want:=map[string]string{"content":"# 标题\r\n\n[事实] 原文  \n","base_revision":"revision-1"}
	if err:=s.Set(key,want);err!=nil {t.Fatal(err)}
	reopened,err:=Open(dir);if err!=nil {t.Fatal(err)}
	var got map[string]string
	if err:=reopened.Get(key,&got);err!=nil {t.Fatal(err)}
	if got["content"]!=want["content"]||got["base_revision"]!=want["base_revision"] {t.Fatalf("draft changed: %#v",got)}
	if err:=reopened.Get("https://other.example:user:123:entry:456",&got);!errors.Is(err,os.ErrNotExist) {t.Fatal("draft leaked across server keys")}
}

func TestUnsafeKeyCannotEscapeStoreAndBadSavePreservesDraft(t *testing.T) {
	dir:=t.TempDir();s,err:=Open(dir);if err!=nil {t.Fatal(err)}
	key:="../../outside:secret?name=你好"
	if err:=s.Set(key,"original");err!=nil {t.Fatal(err)}
	if err:=s.Set(key,make(chan int));err==nil {t.Fatal("invalid value saved")}
	var got string
	if err:=s.Get(key,&got);err!=nil||got!="original" {t.Fatalf("previous draft lost: %q %v",got,err)}
	entries,_:=os.ReadDir(dir)
	if len(entries)!=1||len(entries[0].Name())!=69||strings.Contains(entries[0].Name(),"outside") {t.Fatalf("unsafe filename: %v",entries)}
	if err:=s.Delete(key);err!=nil {t.Fatal(err)}
	if err:=s.Delete(key);err!=nil {t.Fatal("deletion not idempotent")}
	if err:=s.Get(key,&got);!errors.Is(err,os.ErrNotExist) {t.Fatal("deleted draft still present")}
}

func TestCorruptDraftIsReportedAndRetained(t *testing.T) {
	s,err:=Open(t.TempDir());if err!=nil {t.Fatal(err)}
	os.WriteFile(s.filename("draft"),[]byte("{broken"),0o600)
	var got map[string]any
	if err:=s.Get("draft",&got);err==nil {t.Fatal("corrupt draft hidden")}
	data,_:=os.ReadFile(s.filename("draft"));if string(data)!="{broken" {t.Fatal("corrupt draft was destroyed")}
}

func TestAtomicReplacementAndConcurrentSaves(t *testing.T) {
	dir:=t.TempDir();s,err:=Open(dir);if err!=nil {t.Fatal(err)}
	var wg sync.WaitGroup
	for i:=0;i<20;i++ {wg.Add(1);go func(value int){defer wg.Done();if err:=s.Set("shared",map[string]int{"value":value});err!=nil {t.Error(err)}}(i)}
	wg.Wait()
	var got map[string]int
	if err:=s.Get("shared",&got);err!=nil {t.Fatal(err)}
	if got["value"]<0||got["value"]>=20 {t.Fatal(got)}
	entries,_:=os.ReadDir(dir);if len(entries)!=1 {t.Fatal("temporary save files leaked")}
	old:=filepath.Join(dir,"target.json");tmp:=filepath.Join(dir,"new.tmp")
	os.WriteFile(old,[]byte("old"),0o600);os.WriteFile(tmp,[]byte("new"),0o600)
	if err:=ReplaceFile(tmp,old);err!=nil {t.Fatal(err)}
	data,_:=os.ReadFile(old);if string(data)!="new" {t.Fatal("existing destination not replaced")}
	if err:=ReplaceFile(filepath.Join(dir,"missing"),old);err==nil {t.Fatal("missing source accepted")}
	data,_=os.ReadFile(old);if string(data)!="new" {t.Fatal("failed replacement damaged destination")}
}
