package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectImportDirectoryPreservesPathsAndReportsIgnoredFiles(t *testing.T) {
	root:=filepath.Join(t.TempDir(),"knowledge")
	if err:=os.MkdirAll(filepath.Join(root,"common"),0700); err!=nil {t.Fatal(err)}
	for name,content:=range map[string]string{"common/词条.md":"# 词条\n正文", "notes.jsonl":"{}\n", "readme.txt":"unsupported"} {
		if err:=os.WriteFile(filepath.Join(root,filepath.FromSlash(name)),[]byte(content),0600); err!=nil {t.Fatal(err)}
	}
	files,ignored,err:=collectImportFiles([]string{root},true)
	if err!=nil {t.Fatal(err)}
	if len(files)!=2 || files[0].Name!="knowledge/common/词条.md" || files[1].Name!="knowledge/notes.jsonl" {t.Fatalf("lost directory paths: %#v",files)}
	if len(ignored)!=1 || ignored[0]!="knowledge/readme.txt" {t.Fatalf("unsupported files not reported: %#v",ignored)}
}

func TestCollectImportFilesEnforcesLimitsBeforeUpload(t *testing.T) {
	root:=t.TempDir()
	large:=filepath.Join(root,"large.zip")
	f,err:=os.Create(large); if err!=nil {t.Fatal(err)}
	if err=f.Truncate(importMaxBytes+1); err!=nil {f.Close();t.Fatal(err)}
	if err=f.Close();err!=nil{t.Fatal(err)}
	if _,_,err:=collectImportFiles([]string{large},false);err==nil {t.Fatal("oversized file accepted")}
	first:=filepath.Join(root,"first","same.md");second:=filepath.Join(root,"second","same.md")
	for _,path:=range []string{first,second}{
		if err:=os.MkdirAll(filepath.Dir(path),0700);err!=nil{t.Fatal(err)}
		if err:=os.WriteFile(path,[]byte("# Entry"),0600);err!=nil{t.Fatal(err)}
	}
	if _,_,err:=collectImportFiles([]string{first,second},false);err==nil {t.Fatal("duplicate multipart names accepted")}
}

func TestImportSelectionBlocksInvalidGroupAndKeepsIndependentItems(t *testing.T) {
	batch:=object{"items":[]object{
		{"id":"bad","grouped":true,"error":"invalid","classification":"invalid"},
		{"id":"linked","grouped":true,"classification":"changed"},
		{"id":"free","classification":"new"},
		{"id":"duplicate","classification":"duplicate"},
	}}
	eligible:=importSelectable(batch)
	if len(eligible)!=1 || str(eligible[0],"id")!="free" {t.Fatalf("invalid group selectable: %#v",eligible)}
	state:=importsState{}
	state.setActive(batch)
	if len(state.selected)!=1 || !state.selected["free"] {t.Fatalf("wrong initial selection: %#v",state.selected)}
}

func TestReleaseSelectionKeepsGroupsTogetherAndDoesNotSelectBlockedItems(t *testing.T) {
	rows:=[]object{
		{"id":"a","group_id":"group","blockers":[]string{}},
		{"id":"b","group_id":"group","blockers":[]string{}},
		{"id":"blocked","group_id":"group","blockers":[]string{"基线已变化"}},
		{"id":"independent","blockers":[]string{}},
	}
	selected:=selectReleaseCandidate(rows,map[string]bool{"independent":true},rows[0],true)
	if !selected["a"] || !selected["b"] || !selected["independent"] || selected["blocked"] {t.Fatalf("wrong selection: %#v",selected)}
	selected=selectReleaseCandidate(rows,selected,rows[1],false)
	if len(selected)!=1 || !selected["independent"] {t.Fatalf("deselect group changed unrelated item: %#v",selected)}
}
