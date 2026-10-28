// FineSub Review uses native MyGO UI in every window, including updates.
package main

import (
 "context"
 _ "embed"
 "encoding/json"
 "errors"
 flagpkg "flag"
 "fmt"
 "log"
 "os"
 "path/filepath"
 "strconv"
 "time"

 "finesub-review-desktop/internal/client"
 "finesub-review-desktop/internal/localstore"
 "github.com/egoist/mygo"
 "github.com/egoist/mygo/plugins/updater"
 "github.com/egoist/mygo/plugins/updater/native"
 "github.com/egoist/mygo/ui"
)

//go:embed mygo.json
var applicationConfig []byte
const defaultServer = "https://49.234.185.63"
type object = map[string]any
type metadata struct { Name, Version, Identifier string }
func readMetadata() (metadata,error) {
 var m metadata
 if err:=json.Unmarshal(applicationConfig,&m);err!=nil{return m,err}
 if m.Name==""||m.Version==""||m.Identifier==""{return m,errors.New("应用配置缺少名称、版本或标识")}
 return m,nil
}

type systemSecrets struct{ service string }
func(s systemSecrets)Get(key string)([]byte,error){
 b,err:=mygo.Secrets.Service(s.service).Get(key)
 if errors.Is(err,mygo.ErrSecretNotFound){return nil,os.ErrNotExist};return b,err
}
func(s systemSecrets)Set(key string,value []byte)error{return mygo.Secrets.Service(s.service).Set(key,value)}
func(s systemSecrets)Delete(key string)error{return mygo.Secrets.Service(s.service).Delete(key)}

type application struct {
 window *mygo.Window
 client *client.Client
 store *localstore.Store
 secrets client.Secrets
 meta metadata
 ctx context.Context
 cancel context.CancelFunc
 page,server,status,error string
 connected,initialized,accessConfigured,busy bool
 passphrase,username,password string
 adminLogin bool
 user,settings object
 people,notices []object
 lastNoticePoll time.Time
 catalog catalogState
 preferences preferencesState
 review reviewState
 imports importsState
 releases releasesState
}

func main(){
 m,err:=readMetadata();if err!=nil{log.Fatal(err)}
 showVersion:=flagpkg.Bool("version",false,"print version without starting the UI");flagpkg.Parse()
 if *showVersion{fmt.Println(m.Version);return}
 mygo.App.SetName(m.Name)
 if mygo.App.Version()==""{mygo.App.SetVersion(m.Version)}
 if !mygo.App.RequestSingleInstanceLock(){return}
 dir,err:=mygo.App.Path(mygo.PathUserData);if err!=nil{log.Fatal(err)}
 store,err:=localstore.Open(filepath.Join(dir,"native"));if err!=nil{log.Fatal(err)}
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 a:=&application{meta:m,store:store,secrets:systemSecrets{m.Identifier},ctx:ctx,cancel:cancel,server:defaultServer,page:"dashboard",initialized:true}
 var saved struct{Server string}
 if err:=store.Get("preferences",&saved);err==nil&&saved.Server!=""{a.server=saved.Server}else if err!=nil&&!errors.Is(err,os.ErrNotExist){a.error=err.Error()}
 mygo.Use(native.New(updater.Options{Language:"zh-Hans"}))
 mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
  {Label:"FineSub",Submenu:[]*mygo.MenuItem{
   {Label:"刷新",Accelerator:"CmdOrCtrl+R",Click:func(_ *mygo.MenuItem,_ *mygo.Window){a.refreshPage()}},
   updater.MenuItem(),mygo.Separator(),{Role:mygo.RoleQuit,Label:"退出"},
  }},
  {Role:mygo.RoleEditMenu},{Role:mygo.RoleWindowMenu},
 }))
 mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent){if !a.canExit(){e.PreventDefault()}})
 mygo.App.OnSecondInstance(func(_ []string,_ string){if a.window!=nil{a.window.Restore();a.window.Focus()}})
 mygo.App.WhenReady(func(){
  a.window=mygo.NewWindow(mygo.WindowOptions{Title:"FineSub 知识库审核",Width:1280,Height:860,MinWidth:900,MinHeight:640,StateKey:"main",Content:ui.View(a.view)})
  a.window.OnClose(func(e *mygo.CloseEvent){if !a.canExit(){e.PreventDefault()}})
  a.window.OnClosed(func(){cancel()})
  a.connect();go a.tick()
 })
 if err:=mygo.App.Run();err!=nil{log.Print(err)}
 if a.client!=nil{if err:=a.client.Close();err!=nil{log.Print("保存会话失败: ",err)}}
}

// Completion clears busy before running its UI closure so it can start a refresh.
func(a *application)run(label string,work func(context.Context)(func(),error)){
 if a.busy{return};a.busy,a.status,a.error=true,label,""
 ctx:=a.ctx;if ctx==nil{ctx=context.Background()}
 go func(){
  done,err:=work(ctx);if ctx.Err()!=nil{return}
  a.window.Update(func(){
   a.busy=false
   if err!=nil{a.status="";a.error=err.Error();return}
   a.status=label+"完成";if done!=nil{done()}
  })
 }()
}
func(a *application)admin()bool{return str(a.user,"role")=="admin"}
func(a *application)saveDrafts()error{
 if a.store==nil{return nil}
 if err:=a.saveReviewDrafts();err!=nil{return err}
 return a.saveTransferDrafts()
}
func(a *application)canExit()bool{
 var err error
 if a.busy{err=errors.New("操作正在进行，请等待完成后再关闭或重启")
 }else if a.preferencesDirty(){err=errors.New("设置中还有未保存的修改，请先保存或撤销，再关闭或重启")
 }else{err=a.saveDrafts()}
 if err==nil{return true};a.error=err.Error()
 if a.window!=nil{a.window.Focus();a.window.Update(func(){})};return false
}
func(a *application)navigate(page string){
 if a.busy{return};if err:=a.saveDrafts();err!=nil{a.error=err.Error();return}
 a.page,a.error=page,"";a.refreshPage()
}
func(a *application)refreshPage(){
 if a.busy{return};if !a.connected{a.connect();return};if a.user==nil{return}
 switch a.page{
 case "dashboard":a.loadDashboard()
 case "entries","mine":a.loadEntries()
 case "streamer":a.loadStreamers()
 case "imports":a.loadImports()
 case "releases":a.loadReleases()
 case "settings","account":a.loadSettings()
 case "notices":a.loadNotices()
 }
}
func(a *application)connect(){
 address,secrets,store:=a.server,a.secrets,a.store
 a.run("连接审核服务器",func(ctx context.Context)(func(),error){
  next,err:=client.New(address,secrets);if err!=nil{return nil,err}
  var b object
  if err:=next.Get(ctx,"/bootstrap",&b);err!=nil{next.Close();return nil,err}
  if _,ok:=b["initialized"].(bool);!ok{next.Close();return nil,errors.New("该地址不是兼容的 FineSub 审核服务")}
  if _,ok:=b["access_configured"].(bool);!ok{next.Close();return nil,errors.New("该地址不是兼容的 FineSub 审核服务")}
  if err:=store.Set("preferences",map[string]string{"Server":next.Server()});err!=nil{next.Close();return nil,err}
  var identity object;var warning string
  if err:=next.Get(ctx,"/auth/me",&identity);err!=nil&&client.StatusCode(err)!=401{warning=err.Error()}
  return func(){
   if a.client!=nil{_ = a.client.Close()}
   a.client,a.server,a.connected=next,next.Server(),true
   a.initialized,a.accessConfigured=flag(b,"initialized"),flag(b,"access_configured")
   a.user,a.error=obj(identity,"user"),warning
   if a.user!=nil{a.afterAuthentication()}
  },nil
 })
}
func(a *application)enter(){
 if a.passphrase==""{a.error="请输入访问口令";return}
 passphrase,api:=a.passphrase,a.client
 a.run("进入知识库",func(ctx context.Context)(func(),error){
  var r object
  if err:=api.Post(ctx,"/auth/enter",object{"passphrase":passphrase},&r);err!=nil{return nil,err}
  return func(){a.passphrase="";a.user=obj(r,"user");a.resetIdentityViews();a.afterAuthentication()},nil
 })
}
func(a *application)signIn(){
 if a.username==""||a.password==""{a.error="请输入管理员账号和密码";return}
 if err:=a.saveDrafts();err!=nil{a.error=err.Error();return}
 username,password,api:=a.username,a.password,a.client
 a.run("管理员登录",func(ctx context.Context)(func(),error){
  var r object
  if err:=api.Post(ctx,"/auth/login",object{"username":username,"password":password},&r);err!=nil{return nil,err}
  return func(){a.password="";a.adminLogin=false;a.user=obj(r,"user");a.resetIdentityViews();a.afterAuthentication()},nil
 })
}
func(a *application)afterAuthentication(){a.restoreTransferDrafts();a.refreshIdentity()}
func(a *application)refreshIdentity(){
 api:=a.client;if api==nil{return}
 a.run("加载账号",func(ctx context.Context)(func(),error){
  var me,settings object;var people,notices []object
  if err:=api.Get(ctx,"/auth/me",&me);err!=nil{return nil,err}
  if err:=api.Get(ctx,"/settings",&settings);err!=nil{return nil,err}
  if err:=api.Get(ctx,"/users",&people);err!=nil{return nil,err}
  if err:=api.Get(ctx,"/notices",&notices);err!=nil{return nil,err}
  return func(){
   if str(a.user,"id")!=str(obj(me,"user"),"id"){_ = a.saveDrafts();a.resetIdentityViews()}
   a.user,a.people,a.settings,a.notices=obj(me,"user"),people,settings,notices
   a.lastNoticePoll=time.Now();a.restoreTransferDrafts()
   if a.page=="review"{a.page="entries"};a.refreshPage()
  },nil
 })
}
func(a *application)resetIdentityViews(){
 a.people=nil;a.settings=nil;a.notices=nil;a.review=reviewState{};a.catalog=catalogState{}
 a.preferences=preferencesState{};a.imports=importsState{};a.releases=releasesState{};a.page="dashboard"
}
func(a *application)canLeaveIdentity()bool{
 if a.busy{return false};if a.preferencesDirty(){a.error="设置中还有未保存的修改，请先保存或撤销";return false}
 if err:=a.saveDrafts();err!=nil{a.error=err.Error();return false};return true
}
func(a *application)signOut(){
 if !a.canLeaveIdentity(){return};api,admin:=a.client,a.admin()
 a.run("退出",func(ctx context.Context)(func(),error){
  var r object;path:="/auth/leave";if admin{path="/auth/logout"}
  if err:=api.Post(ctx,path,object{},&r);err!=nil{return nil,err}
  return func(){a.resetIdentityViews();a.user=obj(r,"user");a.password="";if a.user!=nil{a.afterAuthentication()}},nil
 })
}
func(a *application)changeServer(){
 if !a.canLeaveIdentity(){return};a.connected=false;a.user=nil;a.adminLogin=false;a.password="";a.passphrase="";a.resetIdentityViews()
}
func(a *application)revalidate(){
 if !a.canLeaveIdentity(){return};api:=a.client
 a.run("验证登录",func(ctx context.Context)(func(),error){
  var r object;err:=api.Get(ctx,"/auth/me",&r)
  if err!=nil&&client.StatusCode(err)!=401{return nil,err}
  return func(){a.resetIdentityViews();a.user=obj(r,"user");if a.user!=nil{a.afterAuthentication()}},nil
 })
}
func(a *application)tick(){
 ticker:=time.NewTicker(2500*time.Millisecond);defer ticker.Stop()
 for{select{
 case <-a.ctx.Done():return
 case <-ticker.C:a.window.Update(func(){
  if a.user==nil||a.busy{return}
  if err:=a.saveDrafts();err!=nil{a.error=err.Error()}
  a.pollTransfers()
  if !a.busy&&time.Since(a.lastNoticePoll)>=30*time.Second{a.loadNotices()}
 })
 }}
}
func(a *application)loadNotices(){
 if a.client==nil||a.user==nil{return};api:=a.client;a.lastNoticePoll=time.Now()
 a.run("刷新通知",func(ctx context.Context)(func(),error){
  var rows []object;if err:=api.Get(ctx,"/notices",&rows);err!=nil{return nil,err}
  return func(){a.notices=rows},nil
 })
}
func(a *application)noticesView(c *ui.Context){
 ui.Scroll(c).Grow(1).Padding(24).Gap(12).Children(func(){
  ui.Text(c,"通知").FontSize(24).Bold()
  if ui.Button(c,"全部标为已读").Disabled(a.busy).Clicked(){
   api:=a.client;a.run("标记通知",func(ctx context.Context)(func(),error){
    if err:=api.Post(ctx,"/notices/read",object{},nil);err!=nil{return nil,err};return func(){a.loadNotices()},nil
   })
  }
  if len(a.notices)==0{ui.Text(c,"暂无通知")}
  for _,n:=range a.notices{n:=n;ui.Column(c.Key(str(n,"id"))).Padding(12).Gap(6).Border(1,c.Theme().Border).Radius(8).Children(func(){
   ui.Text(c,str(n,"text"));ui.Text(c,nativeDate(str(n,"created_at")))
   if id:=str(n,"entry_id");id!=""{if ui.Button(c,"查看词条").Disabled(a.busy).Clicked(){a.openReview(id)}}
  })}
 })
}
func(a *application)view(c *ui.Context){
 ui.Column(c).Fill().Background(c.Theme().Background).Children(func(){
  ui.Row(c.Key("header")).Padding(12,20).Gap(12).AlignItems(ui.Center).BorderWidth(0,0,1,0).BorderColor(c.Theme().Border).Children(func(){
   ui.Text(c,"FineSub 知识库审核").Bold().Grow(1);ui.Text(c,"v"+a.meta.Version)
   if ui.Button(c,"检查更新").Clicked(){updater.CheckForUpdates()}
   if a.connected{if ui.Button(c,"更换服务器").Disabled(a.busy).Clicked(){a.changeServer()}}
  })
  if a.error!=""{ui.Row(c.Key("error")).Padding(10,20).Gap(8).Children(func(){
   ui.Text(c,a.error).Color(c.Theme().Danger).Grow(1)
   if a.connected&&a.user!=nil{if ui.Button(c,"重新验证登录").Disabled(a.busy).Clicked(){a.revalidate()}}
   if ui.Button(c,"关闭提示").Clicked(){a.error=""}
  })}
  if !a.connected{a.connectionView(c)}else if a.user==nil{a.entryView(c)}else if a.adminLogin{a.adminView(c)}else{
   ui.Row(c.Key("workspace")).Grow(1).FillWidth().Children(func(){
    a.sidebar(c)
    ui.Column(c.Key("page-"+a.page)).Grow(1).FillHeight().Children(func(){switch a.page{
     case "entries","mine":a.entriesView(c)
     case "review":a.reviewView(c)
     case "streamer":a.streamerView(c)
     case "imports":a.importsView(c)
     case "releases":a.releasesView(c)
     case "settings","account":a.settingsView(c)
     case "notices":a.noticesView(c)
     default:a.dashboardView(c)
    }})
   })
  }
  ui.Row(c.Key("status")).Padding(8,20).Gap(12).BorderWidth(1,0,0,0).BorderColor(c.Theme().Border).Children(func(){
   text:=a.status;if a.busy{text+="…"};ui.Text(c,text).Grow(1);if a.connected{ui.Text(c,a.server)}
  })
 })
}
func(a *application)sidebar(c *ui.Context){
 ui.Column(c.Key("sidebar")).Width(210).FillHeight().Padding(16).Gap(8).BorderWidth(0,1,0,0).BorderColor(c.Theme().Border).Children(func(){
  ui.Text(c,str(a.settings,"name")).Bold();ui.Text(c,str(a.user,"name"))
  items:=[][2]string{{"dashboard","工作台"},{"entries","知识库"},{"streamer","主播档案"},{"mine","我的审核"}}
  if a.admin(){items=append(items,[2]string{"imports","导入批次"},[2]string{"releases","发布版本"},[2]string{"settings","管理设置"})}
  items=append(items,[2]string{"account","我的账号"},[2]string{"notices",fmt.Sprintf("通知 (%d)",unreadNotices(a.notices))})
  for _,item:=range items{item:=item;label:=item[1];if a.page==item[0]{label="● "+label};if ui.Button(c.Key(item[0]),label).Disabled(a.busy).Clicked(){a.navigate(item[0])}}
  ui.Box(c).Grow(1)
  if !a.admin(){if ui.Button(c,"管理员登录").Disabled(a.busy).Clicked(){if a.canLeaveIdentity(){a.adminLogin=true}}}
  label:="退出知识库";if a.admin(){label="退出管理员"};if ui.Button(c,label).Disabled(a.busy).Clicked(){a.signOut()}
 })
}
func(a *application)connectionView(c *ui.Context){
 ui.Column(c.Key("connection")).Grow(1).FillWidth().Center().Gap(16).Padding(32).Children(func(){
  ui.Text(c,"连接审核服务器").FontSize(28).Bold();ui.Text(c,"连接后输入访问口令，即可开始审核。")
  ui.TextInput(c,&a.server).Label("服务器地址").Placeholder("https://review.example.com").Width(520).Disabled(a.busy)
  if ui.PrimaryButton(c,"连接").Disabled(a.busy).Clicked(){a.connect()}
 })
}
func(a *application)entryView(c *ui.Context){
 ui.Column(c.Key("entry")).Grow(1).FillWidth().Center().Gap(16).Padding(32).Children(func(){
  ui.Text(c,"进入知识库").FontSize(28).Bold()
  if !a.initialized||!a.accessConfigured{ui.Text(c,"服务器尚未完成初始化，请联系管理员设置账号和访问口令。");return}
  if ui.TextInput(c,&a.passphrase).Password().Label("访问口令").Placeholder("请输入访问口令").Width(400).Disabled(a.busy).Submitted(){a.enter()}
  if ui.PrimaryButton(c,"进入知识库").Disabled(a.busy).Clicked(){a.enter()}
 })
}
func(a *application)adminView(c *ui.Context){
 ui.Column(c.Key("admin-login")).Grow(1).FillWidth().Center().Gap(16).Padding(32).Children(func(){
  ui.Text(c,"管理员登录").FontSize(28).Bold()
  ui.TextInput(c,&a.username).Label("管理员账号").Placeholder("管理员账号").Width(400).Disabled(a.busy)
  if ui.TextInput(c,&a.password).Password().Label("管理员密码").Placeholder("密码").Width(400).Disabled(a.busy).Submitted(){a.signIn()}
  ui.Row(c).Gap(10).Children(func(){
   if ui.Button(c,"返回").Disabled(a.busy).Clicked(){a.password="";a.adminLogin=false}
   if ui.PrimaryButton(c,"登录").Disabled(a.busy).Clicked(){a.signIn()}
  })
 })
}
func unreadNotices(rows []object)int{n:=0;for _,r:=range rows{if !flag(r,"read"){n++}};return n}
func valueString(v any)string{if v==nil{return ""};if s,ok:=v.(string);ok{return s};return fmt.Sprint(v)}
func str(r object,k string)string{return valueString(r[k])}
func num(r object,k string)int{switch v:=r[k].(type){case float64:return int(v);case int:return v;case json.Number:n,_:=v.Int64();return int(n);case string:n,_:=strconv.Atoi(v);return n};return 0}
func flag(r object,k string)bool{v,_:=r[k].(bool);return v}
func obj(r object,k string)object{v,_:=r[k].(map[string]any);return v}
func objects(r object,k string)[]object{
 switch v:=r[k].(type){case []object:return v;case []any:rows:=make([]object,0,len(v));for _,item:=range v{if o,ok:=item.(map[string]any);ok{rows=append(rows,o)}};return rows};return nil
}
