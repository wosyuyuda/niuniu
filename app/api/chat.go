package api

import (
	"fmt"
	"math/rand"

	"github.com/gogf/gf-demos/app/model"
	"github.com/gogf/gf/errors/gerror"
	"github.com/gogf/gf/util/gconv"

	"time"

	"github.com/gogf/gf/container/garray"
	"github.com/gogf/gf/container/gmap"
	"github.com/gogf/gf/container/gset"
	"github.com/gogf/gf/encoding/ghtml"
	"github.com/gogf/gf/encoding/gjson"
	"github.com/gogf/gf/frame/g"
	"github.com/gogf/gf/net/ghttp"
	"github.com/gogf/gf/os/gcache"
)

// 聊天管理器
var Chat = &chatApi{}

type chatApi struct{}

const (
	// SendInterval 允许客户端发送聊天消息的间隔时间
	sendInterval = time.Second
)

var (
	users   = gmap.New(true)       // 使用默认的并发安全Map
	names   = gset.NewStrSet(true) // 使用并发安全的Set，用以用户昵称唯一性校验
	cache   = gcache.New()         // 使用特定的缓存对象，不使用全局缓存对象
	basepai = []string{"A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K"}
	color   = []string{"黑桃", "红桃", "梅花", "方块"}
	kin     = []string{"大王", "小王"}
	allpai  = []string{}

	paiusers = gmap.New(true) // 使用默认的并发安全Map
	//painame  = gset.NewStrSet(true) // 使用并发安全的Set，用以用户昵称唯一性校验
)

//初始化全部的牌
func paiinit() []string {
	for _, v := range color {
		for _, i := range basepai {
			allpai = append(allpai, v+i)
		}
	}
	allpai = append(allpai, kin...)
	return allpai
}

// @summary 聊天室首页
// @description 聊天室首页，只显示模板内容。如果当前用户未登录，那么引导跳转到名称设置页面。
// @tags    聊天室
// @produce html
// @router  /chat/index [GET]
// @success 200 {string} string "执行结果"
func (a *chatApi) Index(r *ghttp.Request) {
	view := r.GetView()
	if r.Session.Contains("chat_name") {
		view.Assign("tplMain", "chat/include/chat.html")
	} else {
		view.Assign("tplMain", "chat/include/main.html")
	}
	r.Response.WriteTpl("chat/index.html")
}

// @summary 设置聊天名称页面
// @description 展示设置聊天名称页面，在该页面设置名称，成功后再跳转到聊天室页面。
// @tags    聊天室
// @produce html
// @router  /chat/setname [GET]
// @success 200 {string} string "执行成功后跳转到聊天室页面"
func (a *chatApi) SetName(r *ghttp.Request) {
	var (
		apiReq *model.ChatApiSetNameReq
	)
	if err := r.ParseForm(&apiReq); err != nil {
		r.Session.Set("chat_name_error", gerror.Current(err).Error())
		r.Response.RedirectBack()
	}
	name := ghtml.Entities(apiReq.Name)
	r.Session.Set("chat_name_temp", name)
	if names.Contains(name) {
		r.Session.Set("chat_name_error", "用户昵称已被占用")
		r.Response.RedirectBack()
	} else {
		r.Session.Set("chat_name", name)
		r.Session.Remove("chat_name_temp", "chat_name_error")
		r.Response.RedirectTo("/chat")
	}
}

// @summary WebSocket接口
// @description 通过WebSocket连接该接口发送任意数据。
// @tags    聊天室
// @router  /chat/websocket [POST]
func (a *chatApi) WebSocket(r *ghttp.Request) {
	msg := &model.ChatMsg{}

	// 初始化WebSocket请求
	var (
		ws  *ghttp.WebSocket
		err error
	)
	ws, err = r.WebSocket()
	if err != nil {
		g.Log().Error(err)
		return
	}

	name := r.Session.GetString("chat_name")
	if name == "" {
		name = r.Request.RemoteAddr
	}

	// 初始化时设置用户昵称为当前链接信息
	names.Add(name)
	users.Set(ws, name)

	// 初始化后向所有客户端发送上线消息
	a.writeUserListToClient()

	for {
		// 阻塞读取WS数据
		_, msgByte, err := ws.ReadMessage()
		if err != nil {
			// 如果失败，那么表示断开，这里清除用户信息
			// 为简化演示，这里不实现失败重连机制
			names.Remove(name)
			users.Remove(ws)
			// 通知所有客户端当前用户已下线
			a.writeUserListToClient()
			break
		}
		// JSON参数解析
		if err := gjson.DecodeTo(msgByte, msg); err != nil {
			a.write(ws, model.ChatMsg{
				Type: "error",
				Data: "消息格式不正确: " + err.Error(),
				From: "",
			})
			continue
		}
		// 数据校验
		if err := g.Validator().Ctx(r.Context()).CheckStruct(msg); err != nil {
			a.write(ws, model.ChatMsg{
				Type: "error",
				Data: gerror.Current(err).Error(),
				From: "",
			})
			continue
		}
		msg.From = name

		// 日志记录
		g.Log().Cat("chat").Println(msg)

		// WS操作类型
		switch msg.Type {
		// 发送消息
		case "send":
			// 发送间隔检查
			intervalKey := fmt.Sprintf("%p", ws)
			if ok, _ := cache.SetIfNotExist(intervalKey, struct{}{}, sendInterval); !ok {
				a.write(ws, model.ChatMsg{
					Type: "error",
					Data: "您的消息发送得过于频繁，请休息下再重试",
					From: "",
				})
				continue
			}
			// 有消息时，群发消息
			if msg.Data != nil {
				fmt.Println(gconv.String(msg.Data))
				if gconv.String(msg.Data) == "111" {
					//如果用户输入111,那么返回
					paiusers.Set(ws, name) //把用户加到组里面,如果人数满3人,就开始发牌,并且清空原来的数组
					if paiusers.Size() == 2 {
						//开始发牌
						if err = a.writeGroup1(); err != nil {
							g.Log().Error(err)
						}
					} else if err = a.writeGroup(
						model.ChatMsg{
							Type: "send",
							Data: ghtml.SpecialChars("当前人数" + gconv.String(paiusers.Size())),
							From: ghtml.SpecialChars(msg.From),
						}); err != nil {
						g.Log().Error(err)
					}

				} else if err = a.writeGroup(
					model.ChatMsg{
						Type: "send",
						Data: ghtml.SpecialChars(gconv.String(msg.Data)),
						From: ghtml.SpecialChars(msg.From),
					}); err != nil {
					g.Log().Error(err)
				}
			}
		}
	}
}

//进入发牌
func (a *chatApi) writeGroup1() error {
	pai := paiinit()
	pai1, pai := fapai(pai)
	pai2, pai := fapai(pai)
	n := 0
	fmt.Printf("全部的牌是%+v", pai)
	fmt.Println(paiusers.Size())
	msg := model.ChatMsg{
		Type: "send",
		Data: pai1,
		From: ghtml.SpecialChars("官方发牌员"),
	}
	msg1 := model.ChatMsg{
		Type: "send",
		Data: pai2,
		From: ghtml.SpecialChars("官方发牌员"),
	}
	b, _ := gjson.Encode(msg)
	b1, err := gjson.Encode(msg1)

	mm := [][]byte{b, b1}
	if err != nil {
		return err
	}
	paiusers.RLockFunc(func(m map[interface{}]interface{}) {
		for user := range m {
			user.(*ghttp.WebSocket).WriteMessage(ghttp.WS_MSG_TEXT, []byte(mm[n]))
			n++
		}
	})
	paiusers.Clear()
	return nil
}

func fapai(pai []string) (uspai []string, newpai []string) {
	var num = 1
	for num <= 5 {
		//获取牌的长度,得到一个随机数
		i := ra(len(pai))
		num++
		uspai = append(uspai, pai[i])
		pai = append(pai[:i], pai[i+1:]...)
		fmt.Println(num)
	}
	newpai = pai
	return
}

//得到一个随机数
func ra(i int) (j int) {
	rand.Seed(time.Now().UnixNano())
	j = rand.Intn(i)
	return
}

// 向客户端写入消息。
// 内部方法不会自动注册到路由中。
func (a *chatApi) write(ws *ghttp.WebSocket, msg model.ChatMsg) error {
	msgBytes, err := gjson.Encode(msg)
	if err != nil {
		return err
	}
	return ws.WriteMessage(ghttp.WS_MSG_TEXT, msgBytes)
}

// 向所有客户端群发消息。
// 内部方法不会自动注册到路由中。
func (a *chatApi) writeGroup(msg model.ChatMsg) error {
	b, err := gjson.Encode(msg)
	if err != nil {
		return err
	}
	users.RLockFunc(func(m map[interface{}]interface{}) {
		for user := range m {
			user.(*ghttp.WebSocket).WriteMessage(ghttp.WS_MSG_TEXT, []byte(b))
		}
	})

	return nil
}

// 向客户端返回用户列表。
// 内部方法不会自动注册到路由中。
func (a *chatApi) writeUserListToClient() error {
	array := garray.NewSortedStrArray()
	names.Iterator(func(v string) bool {
		array.Add(v)
		return true
	})
	if err := a.writeGroup(model.ChatMsg{
		Type: "list",
		Data: array.Slice(),
		From: "",
	}); err != nil {
		return err
	}
	return nil
}
