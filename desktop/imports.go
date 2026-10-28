package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"finesub-review-desktop/internal/client"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

const importMaxFiles = 200
const importMaxBytes = 30 * 1024 * 1024

type importsState struct {
	page int
	rows []object
	active object
	selected map[string]bool
	form bool
	name, source, category string
	files []client.UploadFile
	ignored []string
	preview object
	previewMode string
	previewDiff *editor.DiffState
	previewPatch string
	md markdown.Options
}

type importDraft struct {
	Open bool
	Name, Source, Category string
	Files []client.UploadFile
}

var importCategories = []string{"通用知识", "主播档案", "翻译风格", "翻译纠错"}

func importCategory(label string) string {
	return map[string]string{"通用知识":"common", "主播档案":"streamer", "翻译风格":"style", "翻译纠错":"translation"}[label]
}

func transferLabel(value string) string {
	labels := map[string]string{"queued":"排队中", "processing":"处理中", "preview":"待确认", "confirmed":"已导入", "failed":"失败", "ready":"已发布", "new":"新增", "changed":"修改", "duplicate":"重复", "invalid":"无法导入", "renamed":"重命名", "deleted":"删除", "approved":"已通过", "pending":"待裁决", "rejected":"已驳回", "needs_changes":"待补充", "common":"通用知识", "streamer":"主播档案", "style":"翻译风格", "translation":"翻译纠错"}
	if label, ok := labels[value]; ok { return label }
	return value
}

func transferStrings(value any) []string {
	if list, ok := value.([]string); ok { return list }
	var out []string
	if list, ok := value.([]any); ok {
		for _, item := range list { if s, ok := item.(string); ok { out = append(out, s) } }
	}
	return out
}

// collectImportFiles preserves the same root-relative names as a directory
// selection, without following links or silently including unsupported files.
func collectImportFiles(paths []string, directories bool) ([]client.UploadFile, []string, error) {
	var files []client.UploadFile
	var ignored []string
	var total int64
	seen := map[string]bool{}
	add := func(path, name string, info fs.FileInfo) error {
		name = filepath.ToSlash(name)
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() { return fmt.Errorf("不能上传符号链接或非普通文件：%s", name) }
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".zip" && ext != ".md" && ext != ".jsonl" { ignored = append(ignored, name); return nil }
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.Contains(name, "\x00") { return fmt.Errorf("文件路径无效：%s", name) }
		if seen[name] { return fmt.Errorf("选择中存在相同上传路径：%s", name) }
		seen[name] = true
		total += info.Size()
		if total > importMaxBytes { return fmt.Errorf("单批上传总量不能超过 30 MB") }
		if len(files) >= importMaxFiles { return fmt.Errorf("每批最多上传 200 个文件") }
		files = append(files, client.UploadFile{Path:path, Name:name})
		return nil
	}
	for _, selected := range paths {
		path, err := filepath.Abs(selected)
		if err != nil { return nil, nil, err }
		info, err := os.Lstat(path)
		if err != nil { return nil, nil, err }
		if info.Mode()&os.ModeSymlink != 0 { return nil, nil, fmt.Errorf("不能上传符号链接：%s", path) }
		if !directories && !info.IsDir() {
			if err := add(path, filepath.Base(path), info); err != nil { return nil, nil, err }
			continue
		}
		if !info.IsDir() { return nil, nil, fmt.Errorf("请选择目录：%s", path) }
		rootName := filepath.Base(path)
		if rootName == "." || rootName == string(filepath.Separator) { return nil, nil, fmt.Errorf("请选择知识库目录，不能选择磁盘根目录") }
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil { return walkErr }
			if entry.Type()&os.ModeSymlink != 0 { return fmt.Errorf("目录中包含符号链接，请移除后再导入：%s", current) }
			if entry.IsDir() { return nil }
			relative, err := filepath.Rel(path, current)
			if err != nil { return err }
			entryInfo, err := entry.Info()
			if err != nil { return err }
			return add(current, filepath.Join(rootName, relative), entryInfo)
		})
		if err != nil { return nil, nil, err }
	}
	sort.Slice(files, func(i,j int) bool { return files[i].Name < files[j].Name })
	if len(files) == 0 { return nil, ignored, fmt.Errorf("未找到可上传的 ZIP、Markdown 或 JSONL 文件") }
	return files, ignored, nil
}

func importSelectable(batch object) []object {
	items := objects(batch, "items")
	groupBlocked := false
	for _, item := range items { if flag(item,"grouped") && str(item,"error") != "" { groupBlocked = true } }
	var out []object
	for _, item := range items {
		if str(item,"error") == "" && str(item,"classification") != "duplicate" && !(flag(item,"grouped") && groupBlocked) { out = append(out,item) }
	}
	return out
}

func (s *importsState) setActive(batch object) {
	reset := str(s.active,"id") != str(batch,"id") || str(s.active,"status") != str(batch,"status")
	s.active = batch
	if reset || s.selected == nil {
		s.selected = map[string]bool{}
		for _, item := range importSelectable(batch) { s.selected[str(item,"id")] = true }
	}
}

func (s *importsState) choose(item object, checked bool) {
	if s.selected == nil { s.selected = map[string]bool{} }
	for _, candidate := range importSelectable(s.active) {
		if str(candidate,"id") == str(item,"id") || (flag(item,"grouped") && flag(candidate,"grouped")) {
			if checked { s.selected[str(candidate,"id")] = true } else { delete(s.selected,str(candidate,"id")) }
		}
	}
}

func (a *application) loadImports() {
	if a.imports.page < 1 { a.imports.page = 1 }
	page, id := a.imports.page, str(a.imports.active,"id")
	api := a.client
	a.run("正在读取导入批次", func(ctx context.Context)(func(),error) {
		var rows []object
		if err := api.Get(ctx,fmt.Sprintf("/imports?page=%d",page),&rows); err != nil { return nil,err }
		var active object
		if id != "" { if err := api.Get(ctx,"/imports/"+url.PathEscape(id),&active); err != nil { return nil,err } }
		return func(){ a.imports.rows=rows; if active != nil { a.imports.setActive(active) } },nil
	})
}

func (a *application) openImport(id string) {
	api := a.client
	a.run("正在读取批次详情", func(ctx context.Context)(func(),error) {
		var batch object
		if err := api.Get(ctx,"/imports/"+url.PathEscape(id),&batch); err != nil { return nil,err }
		return func(){ a.imports.setActive(batch); a.imports.preview=nil },nil
	})
}

func (a *application) pickImportFiles(directories bool) {
	window := a.window
	a.run("正在选择导入文件",func(ctx context.Context)(func(),error) {
		title := "选择 ZIP、Markdown 或 JSONL 文件"
		if directories { title = "选择知识库目录" }
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent:window, Title:title, Directory:directories, Multiple:!directories, Filters:[]mygo.FileFilter{{Name:"知识库文件",Extensions:[]string{"zip","md","jsonl"}}}})
		if err != nil { return nil,err }
		if len(paths)==0 { return nil,nil }
		files, ignored, err := collectImportFiles(paths,directories)
		if err != nil { return nil,err }
		return func(){ a.setImportFiles(files,ignored) },nil
	})
}

func (a *application) setImportFiles(files []client.UploadFile, ignored []string) {
	a.imports.files,a.imports.ignored=files,ignored
	if a.imports.name=="" {
		a.imports.name="知识库导入 · "+time.Now().Format("2006-01-02")
		if len(files)==1 { a.imports.name=strings.TrimSuffix(filepath.Base(files[0].Name),filepath.Ext(files[0].Name)) }
	}
	a.persistTransferDrafts()
}

func (a *application) acceptDroppedImports(paths []string) {
	if a.busy || !a.admin() { return }
	paths=append([]string(nil),paths...)
	a.run("正在检查拖入的文件",func(ctx context.Context)(func(),error){
		files,ignored,err:=collectImportFiles(paths,false)
		if err!=nil {return nil,err}
		return func(){a.setImportFiles(files,ignored)},nil
	})
}

func (a *application) uploadImport() {
	s := &a.imports
	if !a.admin() || len(s.files)==0 { return }
	if strings.TrimSpace(s.name)=="" || utf8.RuneCountInString(s.name)>160 || utf8.RuneCountInString(s.source)>10000 { a.error="请填写不超过 160 字的批次名称，来源备注不超过 10000 字"; return }
	files := append([]client.UploadFile(nil),s.files...)
	fields := map[string]string{"name":s.name,"source":s.source,"category":importCategory(s.category)}
	if fields["category"]=="" { fields["category"]="common" }
	api := a.client
	a.run("正在上传并创建预检任务",func(ctx context.Context)(func(),error) {
		var result object
		if err := api.Upload(ctx,"/imports",files,fields,&result); err != nil { return nil,err }
		return func(){
			a.imports.form=false; a.imports.files=nil; a.imports.ignored=nil; a.imports.name=""; a.imports.source=""
			a.imports.active=object{"id":str(result,"id"),"name":fields["name"],"status":"queued"}
			a.status="文件已上传，正在执行预检"; a.persistTransferDrafts(); a.loadImports()
		},nil
	})
}

func (a *application) confirmImport() {
	if !a.admin() || str(a.imports.active,"status")!="preview" { return }
	var ids []string
	var names []string
	for _, item := range importSelectable(a.imports.active) { if a.imports.selected[str(item,"id")] { ids=append(ids,str(item,"id")); names=append(names,str(item,"path")) } }
	if len(ids)==0 { return }
	id, api, window := str(a.imports.active,"id"),a.client,a.window
	a.run("确认导入",func(ctx context.Context)(func(),error) {
		answer,err := mygo.Dialog.Message(mygo.MessageOptions{Parent:window, Type:mygo.MessageQuestion, Title:"确认导入", Message:fmt.Sprintf("将 %d 项导入为待裁决内容？",len(ids)), Detail:strings.Join(names,"\n"), Buttons:[]string{"取消","确认导入"}, DefaultButton:0, CancelButton:0})
		if err!=nil { return nil,err }; if answer.Button!=1 { return nil,nil }
		var result object
		if err := api.Post(ctx,"/imports/"+url.PathEscape(id)+"/confirm",object{"item_ids":ids},&result); err!=nil { return nil,err }
		return func(){ a.status=fmt.Sprintf("已生成 %d 个待裁决项",num(result,"created")); a.imports.active["status"]="confirmed"; a.loadImports() },nil
	})
}

func (a *application) previewImportItem(id string) {
	api := a.client
	a.run("正在加载预检内容",func(ctx context.Context)(func(),error) {
		var preview object
		if err:=api.Get(ctx,"/import-items/"+url.PathEscape(id),&preview); err!=nil { return nil,err }
		patch := strings.Join(transferStrings(preview["diff"]),"\n")+"\n"
		var diff *editor.DiffState
		if parsed,err:=editor.ParsePatch(patch); err==nil && len(parsed)>0 { diff=&editor.DiffState{}; diff.SetPatch(parsed[0]) }
		return func(){ a.imports.preview=preview; a.imports.previewMode="差异"; a.imports.previewPatch=patch; a.imports.previewDiff=diff },nil
	})
}

func (a *application) retryTransferJob(id string, importJob bool) {
	if id=="" || !a.admin() { return }
	api := a.client
	a.run("正在重试后台任务",func(ctx context.Context)(func(),error) {
		var result object
		if err:=api.Post(ctx,"/jobs/"+url.PathEscape(id)+"/retry",object{},&result); err!=nil { return nil,err }
		return func(){ a.status="任务已重新排队"; if importJob { a.loadImports() } else { a.loadReleases() } },nil
	})
}

func (a *application) downloadTransfer(path, name string) {
	if !a.admin() { return }
	api,window := a.client,a.window
	name=filepath.Base(strings.ReplaceAll(name,"\\","/"))
	if name=="" || name=="." { name="download.zip" }
	a.run("正在保存文件",func(ctx context.Context)(func(),error) {
		destination,err:=mygo.Dialog.Save(mygo.SaveDialogOptions{Parent:window,Title:"保存文件",DefaultPath:name})
		if err!=nil { return nil,err }; if destination=="" { return nil,nil }
		if err:=api.Download(ctx,path,destination); err!=nil { return nil,err }
		return func(){ a.status="文件已保存至 "+destination },nil
	})
}

func (a *application) importsView(c *ui.Context) {
	s:=&a.imports
	if s.category=="" { s.category=importCategories[0] }
	ui.Column(c).Fill().Gap(12).Children(func(){
		ui.Row(c).Gap(8).Children(func(){
			ui.Text(c,"导入批次").FontSize(24).Bold().Grow(1)
			if ui.Button(c,"刷新").Disabled(a.busy).Clicked(){ a.loadImports() }
			if a.admin() && ui.PrimaryButton(c,"新建导入").Disabled(a.busy).Clicked(){ s.form=true; a.persistTransferDrafts() }
		})
		if s.form && a.admin() { a.importFormView(c); return }
		if s.preview!=nil { a.importPreviewView(c); return }
		ui.Row(c).Grow(1).Gap(16).Children(func(){
			ui.Column(c).Width(280).Gap(8).Children(func(){
				ui.Scroll(c).Grow(1).Gap(8).Children(func(){
					if len(s.rows)==0 { ui.Text(c,"暂无导入批次") }
					for _,batch:=range s.rows { ui.Column(c.Key(str(batch,"id"))).Padding(10).Gap(4).Children(func(){
						if ui.Button(c,str(batch,"name")).Disabled(a.busy).Clicked(){ a.openImport(str(batch,"id")) }
						ui.Text(c,transferLabel(str(batch,"status"))+" · "+str(batch,"created_at")).FontSize(12)
						if str(batch,"source")!="" { ui.Text(c,str(batch,"source")).MaxLines(2) }
					}) }
				})
				ui.Row(c).Gap(8).Children(func(){
					if ui.Button(c,"上一页").Disabled(a.busy || s.page<=1).Clicked(){ s.page--; a.loadImports() }
					ui.Textf(c,"第 %d 页",max(1,s.page))
					if ui.Button(c,"下一页").Disabled(a.busy || len(s.rows)<30).Clicked(){ s.page++; a.loadImports() }
				})
			})
			ui.Scroll(c).Grow(1).Gap(12).Children(func(){ a.importDetailView(c) })
		})
	})
}

func (a *application) importFormView(c *ui.Context) {
	s:=&a.imports
	ui.Scroll(c).Grow(1).Gap(12).Children(func(){
		ui.Text(c,"导入待审核知识").FontSize(20).Bold()
		if ui.TextInput(c,&s.name).Label("批次名称").Placeholder("批次名称").Disabled(a.busy).Changed(){ a.persistTransferDrafts() }
		ui.Row(c).Gap(8).Children(func(){
			if ui.Button(c,"选择文件").Disabled(a.busy).Clicked(){ a.pickImportFiles(false) }
			if ui.Button(c,"选择目录").Disabled(a.busy).Clicked(){ a.pickImportFiles(true) }
			ui.Textf(c,"已选择 %d 个文件",len(s.files))
		})
		ui.Text(c,"ZIP / Markdown / JSONL · 每批最多 200 个文件、30 MB；目录保持相对路径。")
		zone:=ui.Column(c).Height(72).Padding(16).Border(1,c.Theme().Border)
		if paths:=zone.DroppedFiles();len(paths)>0 && !a.busy {a.acceptDroppedImports(paths)}
		if zone.FileDragOver(){zone.Border(2,c.Theme().Accent)}
		zone.Children(func(){ui.Text(c,"也可以将文件或目录从资源管理器拖到这里。")})
		for _,file:=range s.files { ui.Text(c,file.Name).FontSize(12) }
		if len(s.ignored)>0 { ui.Textf(c,"已跳过 %d 个不支持的文件：%s",len(s.ignored),strings.Join(s.ignored,"、")).TextColor(c.Theme().Danger) }
		if ui.Select(c,&s.category,importCategories).Label("单独 Markdown 文件所属分类").Disabled(a.busy).Changed(){ a.persistTransferDrafts() }
		if ui.TextArea(c,&s.source).Height(110).Label("来源与备注").Placeholder("节目期数 / 字幕文件 / 来源").Disabled(a.busy).Changed(){ a.persistTransferDrafts() }
		ui.Row(c).Gap(8).Children(func(){
			if ui.Button(c,"返回批次（保留草稿）").Disabled(a.busy).Clicked(){ s.form=false; a.persistTransferDrafts() }
			if ui.PrimaryButton(c,"上传并预检").Disabled(a.busy || len(s.files)==0 || strings.TrimSpace(s.name)=="").Clicked(){ a.uploadImport() }
		})
	})
}

func (a *application) importDetailView(c *ui.Context) {
	s:=&a.imports; b:=s.active
	if b==nil { ui.Text(c,"选择一个导入批次查看预检结果"); return }
	ui.Text(c,str(b,"name")).FontSize(20).Bold()
	ui.Text(c,transferLabel(str(b,"status")))
	if str(b,"source")!="" { ui.Text(c,str(b,"source")).Selectable() }
	if str(b,"status")=="confirmed" && ui.Button(c,"审核这一批").Disabled(a.busy).Clicked(){ a.openBatchEntries(str(b,"id")) }
	if str(b,"status")=="queued" || str(b,"status")=="processing" { ui.Text(c,"正在执行导入预检，结果会自动刷新。") }
	if str(b,"status")=="failed" {
		ui.Text(c,str(obj(b,"job"),"error")).TextColor(c.Theme().Danger).Selectable()
		if a.admin() && ui.Button(c,"重试导入").Disabled(a.busy).Clicked(){ a.retryTransferJob(str(obj(b,"job"),"id"),true) }
	}
	for _,warning:=range transferStrings(b["warnings"]) { ui.Text(c,warning).Selectable() }
	items:=objects(b,"items"); eligible:=importSelectable(b); allowed:=map[string]bool{}
	counts:=map[string]int{}
	for _, item:=range items { counts[str(item,"classification")]++ }
	if len(items)>0 { ui.Textf(c,"新增 %d · 修改 %d · 重复 %d · 无效 %d · 重命名 %d · 删除 %d",counts["new"],counts["changed"],counts["duplicate"],counts["invalid"],counts["renamed"],counts["deleted"]) }
	for _,item:=range eligible { allowed[str(item,"id")]=true }
	if a.admin() && str(b,"status")=="preview" {
		all:=len(eligible)>0 && len(s.selected)==len(eligible)
		if ui.Checkbox(c,&all,"选择全部有效项").Disabled(a.busy || len(eligible)==0).Changed(){ s.selected=map[string]bool{}; if all { for id:=range allowed { s.selected[id]=true } } }
		if ui.PrimaryButton(c,fmt.Sprintf("确认导入 %d 项",len(s.selected))).Disabled(a.busy || len(s.selected)==0).Clicked(){ a.confirmImport() }
	}
	for _,item:=range items { ui.Column(c.Key(str(item,"id"))).Padding(10).Gap(5).Children(func(){
		ui.Row(c).Gap(8).Children(func(){
			if a.admin() && str(b,"status")=="preview" { checked:=s.selected[str(item,"id")]; if ui.Checkbox(c,&checked,"选择 "+str(item,"path")).Disabled(a.busy || !allowed[str(item,"id")]).Changed(){ s.choose(item,checked) } } else { ui.Text(c,str(item,"path")).Grow(1) }
			ui.Text(c,transferLabel(str(item,"classification")))
			if ui.Button(c,"预览").Disabled(a.busy).Clicked(){ a.previewImportItem(str(item,"id")) }
		})
		if flag(item,"grouped") { ui.Text(c,"关联变更组：同组项一起确认").FontSize(12) }
		if str(item,"error")!="" { ui.Text(c,str(item,"error")).TextColor(c.Theme().Danger).Selectable() }
		if flag(item,"grouped") && !allowed[str(item,"id")] && str(item,"error")=="" && str(item,"classification")!="duplicate" { ui.Text(c,"同组其他条目校验失败，请修正后重新上传。").TextColor(c.Theme().Danger) }
		for _,warning:=range transferStrings(item["warnings"]) { ui.Text(c,warning).FontSize(12).Selectable() }
	}) }
	if str(b,"status")=="preview" && len(items)==0 { ui.Text(c,"没有可导入的知识正文") }
	if a.admin() && len(objects(b,"assets"))>0 {
		ui.Text(c,"原始上传文件").Bold()
		for _,asset:=range objects(b,"assets") { if ui.Button(c,"下载 "+str(asset,"filename")).Disabled(a.busy).Clicked(){ a.downloadTransfer("/assets/"+url.PathEscape(str(asset,"id"))+"/download",str(asset,"filename")) } }
	}
}

func (a *application) importPreviewView(c *ui.Context) {
	s:=&a.imports
	ui.Row(c).Gap(8).Children(func(){
		ui.Text(c,str(s.preview,"path")).Bold().Grow(1)
		ui.Select(c,&s.previewMode,[]string{"差异","正文","原文"}).Label("预览方式")
		if ui.Button(c,"返回预检").Clicked(){ s.preview=nil }
	})
	if s.preview==nil { return }
	if s.previewMode=="差异" && s.previewDiff!=nil { editor.DiffView(c,s.previewDiff).Grow(1).FillWidth(); return }
	ui.Scroll(c).Grow(1).Gap(8).Children(func(){
		switch s.previewMode {
		case "正文": markdown.View(c,str(s.preview,"content"),&s.md)
		case "原文": ui.Text(c,str(s.preview,"content")).Font("monospace").Selectable()
		default: if strings.TrimSpace(s.previewPatch)=="" { ui.Text(c,"此版本与基础版本没有正文差异。") } else { ui.Text(c,s.previewPatch).Font("monospace").Selectable() }
		}
	})
}

func (a *application) transferDraftKey() string {
	return a.client.Server()+":"+str(a.user,"id")+":native-transfers"
}

func (a *application) saveTransferDrafts() error {
	if a.store==nil || a.client==nil || str(a.user,"id")=="" { return nil }
	return a.store.Set(a.transferDraftKey(),struct{ Import importDraft; Release releaseDraft }{
		Import:importDraft{Open:a.imports.form,Name:a.imports.name,Source:a.imports.source,Category:a.imports.category,Files:a.imports.files},
		Release:releaseDraft{Open:a.releases.form,Name:a.releases.name,Notes:a.releases.notes,Rollback:a.releases.rollback,Selected:a.releases.selected},
	})
}

func (a *application) persistTransferDrafts() {
	if err:=a.saveTransferDrafts(); err!=nil { a.error="无法保存导入/发布草稿："+err.Error() }
}

func (a *application) restoreTransferDrafts() {
	a.imports=importsState{page:1,category:importCategories[0]}
	a.releases=releasesState{}
	if a.store==nil || a.client==nil || str(a.user,"id")=="" { return }
	var draft struct{Import importDraft; Release releaseDraft}
	if err:=a.store.Get(a.transferDraftKey(),&draft); err!=nil { if !errors.Is(err,os.ErrNotExist){ a.error="无法恢复导入/发布草稿："+err.Error() }; return }
	a.imports.form=draft.Import.Open; a.imports.name=draft.Import.Name; a.imports.source=draft.Import.Source; a.imports.files=draft.Import.Files
	if importCategory(draft.Import.Category)!="" { a.imports.category=draft.Import.Category }
	a.releases.form=draft.Release.Open; a.releases.name=draft.Release.Name; a.releases.notes=draft.Release.Notes; a.releases.rollback=draft.Release.Rollback; a.releases.selected=draft.Release.Selected
}

// Called on the UI thread by the app's shared ticker. It never overlaps a
// write or triggers a retry; failed jobs require an explicit user action.
func (a *application) pollTransfers() {
	if a.busy || a.client==nil || str(a.user,"id")=="" { return }
	if a.page=="imports" { status:=str(a.imports.active,"status"); if status=="queued" || status=="processing" { a.loadImports() }; return }
	if a.page=="releases" && releaseProcessing(a.releases.rows) { a.loadReleases() }
}
