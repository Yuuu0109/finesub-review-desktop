package main

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type releasesState struct {
	rows []object
	candidates []object
	selected map[string]bool
	form bool
	name, notes string
	rollback object
	loaded bool
	loadError string
}

type releaseDraft struct {
	Open bool
	Name, Notes string
	Rollback object
	Selected map[string]bool
}

func releaseProcessing(rows []object) bool {
	for _,row:=range rows { if status:=str(row,"status"); status=="queued" || status=="processing" { return true } }
	return false
}

func releaseEligible(candidate object) bool { return len(transferStrings(candidate["blockers"]))==0 }

// Selection operates only on eligible current revisions. Every eligible member
// of a related change group follows the user's toggle; the server rechecks the
// whole group and its publication baseline when the release is created.
func selectReleaseCandidate(candidates []object, selected map[string]bool, item object, checked bool) map[string]bool {
	if selected==nil { selected=map[string]bool{} }
	if !releaseEligible(item) { return selected }
	for _,candidate:=range candidates {
		if !releaseEligible(candidate) { continue }
		if str(candidate,"id")==str(item,"id") || (str(item,"group_id")!="" && str(candidate,"group_id")==str(item,"group_id")) {
			if checked { selected[str(candidate,"id")]=true } else { delete(selected,str(candidate,"id")) }
		}
	}
	return selected
}

func (a *application) loadReleases() {
	if a.busy { return }
	a.releases.loaded=false
	api,admin:=a.client,a.admin()
	a.run("正在读取发布版本",func(ctx context.Context)(func(),error){
		var rows,candidates []object
		if err:=api.Get(ctx,"/releases",&rows); err!=nil { return func(){a.releases.loadError=err.Error()},err }
		if admin { if err:=api.Get(ctx,"/releases/candidates",&candidates); err!=nil { return func(){a.releases.loadError=err.Error()},err } }
		return func(){
			s:=&a.releases
			s.rows=rows; s.candidates=candidates; s.loaded=true; s.loadError=""
			eligible:=map[string]bool{}
			for _,candidate:=range candidates { if releaseEligible(candidate){ eligible[str(candidate,"id")]=true } }
			for id:=range s.selected { if !eligible[id] { delete(s.selected,id) } }
			if s.selected==nil { s.selected=map[string]bool{} }
			a.persistTransferDrafts()
		},nil
	})
}

func (a *application) startRelease(rollback object) {
	if !a.admin(){return}
	s:=&a.releases
	if s.name!="" && str(s.rollback,"id")==str(rollback,"id") {
		s.form=true
		a.persistTransferDrafts()
		return
	}
	if s.name!="" || s.notes!="" {
		window:=a.window
		a.run("切换发布草稿",func(ctx context.Context)(func(),error){
			answer,err:=mygo.Dialog.Message(mygo.MessageOptions{Parent:window,Title:"切换发布内容",Type:mygo.MessageQuestion,Message:"要丢弃当前未提交的发布说明，并开始这次发布吗？",Buttons:[]string{"保留草稿","丢弃并继续"},DefaultButton:0,CancelButton:0})
			if err!=nil{return nil,err}; if answer.Button!=1{return nil,nil}
			return func(){a.releases.name="";a.releases.notes="";a.startRelease(rollback)},nil
		})
		return
	}
	s.rollback=rollback; s.form=true
	if rollback!=nil { s.name="回退至 "+str(rollback,"name") } else { s.name="知识库 · "+time.Now().Format("2006-01-02") }
	s.notes=""
	a.persistTransferDrafts()
}

func (a *application) publishRelease() {
	s:=&a.releases
	if !a.admin() || !s.loaded || s.loadError!="" || releaseProcessing(s.rows){return}
	if strings.TrimSpace(s.name)=="" || utf8.RuneCountInString(s.name)>120 { a.error="请填写不超过 120 字的版本名称"; return }
	if utf8.RuneCountInString(s.notes)>10000 { a.error="发布说明不能超过 10000 字"; return }
	rollbackID:=str(s.rollback,"id")
	if rollbackID!="" && strings.TrimSpace(s.notes)=="" { a.error="请填写回退原因"; return }
	var ids,names []string
	if rollbackID=="" {
		for _,candidate:=range s.candidates { if s.selected[str(candidate,"id")] && releaseEligible(candidate) { ids=append(ids,str(candidate,"id")); names=append(names,str(candidate,"path")) } }
		if len(ids)==0 { a.error="请选择至少一个可发布变更"; return }
	}
	name,notes,api,window:=s.name,s.notes,a.client,a.window
	var rollback any
	if rollbackID!="" { rollback=rollbackID }
	payload:=object{"name":name,"notes":notes,"revision_ids":ids,"rollback_id":rollback}
	if ids==nil { payload["revision_ids"]=[]string{} }
	message:=fmt.Sprintf("确认将 %d 个变更生成完整知识库发布包？",len(ids))
	if rollbackID!="" { message="确认将正式知识库回退为「"+str(s.rollback,"name")+"」的完整内容？" }
	a.run("正在创建发布任务",func(ctx context.Context)(func(),error){
		answer,err:=mygo.Dialog.Message(mygo.MessageOptions{Parent:window,Title:"确认发布",Type:mygo.MessageQuestion,Message:message,Detail:notes+"\n"+strings.Join(names,"\n"),Buttons:[]string{"取消","确认生成"},DefaultButton:0,CancelButton:0})
		if err!=nil{return nil,err}; if answer.Button!=1{return nil,nil}
		var result object
		if err:=api.Post(ctx,"/releases",payload,&result); err!=nil{return nil,err}
		return func(){
			s:=&a.releases
			s.form=false; s.selected=map[string]bool{}; s.name=""; s.notes=""; s.rollback=nil
			s.rows=append([]object{{"id":str(result,"id"),"name":name,"status":"queued"}},s.rows...)
			a.status="发布任务已创建，正在生成并校验完整知识库"; a.persistTransferDrafts(); a.loadReleases()
		},nil
	})
}

func (a *application) releasesView(c *ui.Context) {
	s:=&a.releases
	ui.Column(c).Fill().Gap(12).Children(func(){
		ui.Row(c).Gap(8).Children(func(){
			ui.Text(c,"发布版本").FontSize(24).Bold().Grow(1)
			if ui.Button(c,"刷新").Disabled(a.busy).Clicked(){ a.loadReleases() }
		})
		if s.form && a.admin() { a.releaseFormView(c); return }
		ui.Scroll(c).Grow(1).Gap(16).Children(func(){
			if s.loadError!="" { ui.Text(c,s.loadError).TextColor(c.Theme().Danger).Selectable() }
			if releaseProcessing(s.rows) { ui.Text(c,"正在生成发布包，任务结果会自动刷新。") }
			if a.admin() { a.releaseCandidatesView(c) }
			ui.Textf(c,"历史发布 · %d 个版本",len(s.rows)).FontSize(20).Bold()
			if len(s.rows)==0 { ui.Text(c,"还没有发布版本") }
			for _,row:=range s.rows { a.releaseCardView(c,row) }
		})
	})
}

func (a *application) releaseCandidatesView(c *ui.Context) {
	s:=&a.releases
	if s.selected==nil {s.selected=map[string]bool{}}
	ui.Row(c).Gap(8).Children(func(){
		ui.Textf(c,"待发布变更 · %d",len(s.candidates)).FontSize(20).Bold().Grow(1)
		if ui.PrimaryButton(c,fmt.Sprintf("创建发布 · %d",len(s.selected))).Disabled(a.busy || !s.loaded || s.loadError!="" || len(s.selected)==0 || releaseProcessing(s.rows)).Clicked(){a.startRelease(nil)}
	})
	eligible:=0
	for _,item:=range s.candidates {if releaseEligible(item){eligible++}}
	all:=eligible>0 && len(s.selected)==eligible
	if ui.Checkbox(c,&all,fmt.Sprintf("选择全部可发布变更（%d）",eligible)).Disabled(a.busy || eligible==0).Changed(){
		s.selected=map[string]bool{}
		if all {for _,item:=range s.candidates {if releaseEligible(item){s.selected[str(item,"id")]=true}}}
		a.persistTransferDrafts()
	}
	if len(s.candidates)==0 {ui.Text(c,"暂无待发布变更"); if ui.Button(c,"前往审核").Disabled(a.busy).Clicked(){a.navigate("entries")}}
	for _,item:=range s.candidates {ui.Column(c.Key(str(item,"id"))).Padding(12).Gap(6).Children(func(){
		checked:=s.selected[str(item,"id")]
		if ui.Checkbox(c,&checked,str(item,"title")).Disabled(a.busy || !releaseEligible(item)).Changed(){s.selected=selectReleaseCandidate(s.candidates,s.selected,item,checked); a.persistTransferDrafts()}
		operation:="已通过"
		if str(item,"operation")=="delete"{operation="拟删除"}
		ui.Textf(c,"%s · v%d · %s · %s",transferLabel(str(item,"category")),num(item,"number"),operation,str(item,"path")).FontSize(12)
		if str(item,"group_id")!="" {ui.Text(c,"关联变更组：整组通过并一起发布").FontSize(12)}
		for _,reason:=range transferStrings(item["blockers"]){ui.Text(c,reason).TextColor(c.Theme().Danger).Selectable()}
		ui.Row(c).Gap(8).Children(func(){
			if ui.Button(c,"查看审核项").Disabled(a.busy).Clicked(){a.openReview(str(item,"entry_id"))}
		})
		if !releaseEligible(item){
			for _,related:=range objects(item,"related"){if str(related,"status")!="approved"{if ui.Button(c,"审核关联项："+str(related,"title")).Disabled(a.busy).Clicked(){a.openReview(str(related,"entry_id"))}}}
		}
	})}
}

func (a *application) releaseCardView(c *ui.Context,row object) {
	ui.Column(c.Key(str(row,"id"))).Padding(12).Gap(8).Children(func(){
		ui.Text(c,str(row,"name")).FontSize(18).Bold()
		state:=transferLabel(str(row,"status")); if flag(row,"current"){state+=" · 当前正式版本"}; if str(row,"rollback_of")!=""{state+=" · 回退发布"}
		ui.Text(c,state)
		if str(row,"notes")!=""{ui.Text(c,str(row,"notes")).Selectable()}
		report:=obj(row,"report")
		ui.Textf(c,"%s · %d 个知识记录 · %d 个导出文件",str(row,"created_at"),num(row,"count"),num(report,"files")).FontSize(12)
		for _,issue:=range transferStrings(report["errors"]){ui.Text(c,issue).TextColor(c.Theme().Danger).Selectable()}
		if flag(report,"native_import_requires_compat_tool"){ui.Text(c,"此版本需使用专用兼容工具导入原生知识库。")}
		for _,warning:=range transferStrings(report["warnings"]){ui.Text(c,warning).Selectable()}
		if !a.admin(){return}
		ui.Row(c).Gap(8).Children(func(){
			id:=str(row,"id")
			switch str(row,"status"){
			case "ready":
				filename:="finesub-"+id+".zip"
				if ui.PrimaryButton(c,"下载知识库包").Disabled(a.busy).Clicked(){a.downloadTransfer("/releases/"+url.PathEscape(id)+"/download",filename)}
				if ui.Button(c,"下载审核归档").Disabled(a.busy).Clicked(){a.downloadTransfer("/releases/"+url.PathEscape(id)+"/download?archive=true","finesub-"+id+"-audit.zip")}
				if !flag(row,"current") && ui.Button(c,"回退到此版本").Disabled(a.busy || releaseProcessing(a.releases.rows) || !a.releases.loaded || a.releases.loadError!="").Clicked(){a.startRelease(row)}
			case "failed":
				if ui.Button(c,"重试生成").Disabled(a.busy || str(row,"job_id")=="").Clicked(){a.retryTransferJob(str(row,"job_id"),false)}
				if str(row,"job_id")==""{ui.Text(c,"任务记录缺失")}
			default:ui.Text(c,"等待后台任务完成")
			}
		})
	})
}

func (a *application) releaseFormView(c *ui.Context) {
	s:=&a.releases
	ui.Scroll(c).Grow(1).Gap(12).Children(func(){
		title:="创建发布版本"
		if s.rollback!=nil {title="回退正式知识库"}
		ui.Text(c,title).FontSize(20).Bold()
		if s.rollback!=nil {ui.Text(c,"正式知识库将恢复为「"+str(s.rollback,"name")+"」的完整内容。")}
		if ui.TextInput(c,&s.name).Label("版本名称").Disabled(a.busy).Changed(){a.persistTransferDrafts()}
		if ui.TextArea(c,&s.notes).Height(140).Label("发布说明 / 回退原因").Disabled(a.busy).Changed(){a.persistTransferDrafts()}
		if s.rollback==nil {
			var names []string
			for _,item:=range s.candidates {if s.selected[str(item,"id")]{names=append(names,fmt.Sprintf("%s · v%d",str(item,"path"),num(item,"number")))}}
			sort.Strings(names)
			ui.Textf(c,"本次选中 %d 个变更",len(names)).Bold()
			for _,name:=range names{ui.Text(c,name)}
		}
		ui.Row(c).Gap(8).Children(func(){
			if ui.Button(c,"返回版本列表（保留草稿）").Disabled(a.busy).Clicked(){s.form=false; a.persistTransferDrafts()}
			if ui.PrimaryButton(c,"确认并生成发布包").Disabled(a.busy || !s.loaded || s.loadError!="" || releaseProcessing(s.rows)).Clicked(){a.publishRelease()}
		})
	})
}
