package api

import (
	"fmt"
	"math/rand"
	"strings"

	"niuniu/app/model"

	"github.com/gogf/gf/errors/gerror"
	"github.com/gogf/gf/util/gconv"

	"time"

	"niuniu/library/response"

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
	CachePaiName = "pai"
)

type UserPai struct {
	Name      string
	Num       int8        //点数,0-9为点数,10为牛牛,11为五朵金花
	WinAndLos int8        //输还是赢,1胜,2负
	Multiple  int8        //倍数
	Max       string      //最大的牌
	MaxNum    int         //最大牌的数值,52个
	Pai       []string    //手上具体牌的数据
	PaiNum    []int       //每个牌的点数
	User      interface{} //具体的用户,用来发送消息的接口
}

var (
	users   = gmap.New(true)       // 使用默认的并发安全Map
	names   = gset.NewStrSet(true) // 使用并发安全的Set，用以用户昵称唯一性校验
	cache   = gcache.New()         // 使用特定的缓存对象，不使用全局缓存对象
	basepai = []string{"A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K"}
	color   = []string{"黑桃", "红桃", "梅花", "方块"}
	kin     = []string{"大王", "小王"}

	paiusers = gmap.New(true) // 使用默认的并发安全Map
	//painame  = gset.NewStrSet(true) // 使用并发安全的Set，用以用户昵称唯一性校验
)

//初始化全部的牌,isd判断是否包含大小王,默认包含,如果值为1
func paiinit(isd ...int) []string {
	allpai := []string{}
	for _, v := range color {
		for _, i := range basepai {
			allpai = append(allpai, v+i)
		}
	}
	if len(isd) == 0 {
		allpai = append(allpai, kin...)
	}
	//fmt.Println("初始化牌", allpai)
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
	if users.Size() > 3 {
		response.JsonExit(r, 0, "ok")
		return
	}
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
				dd := gconv.String(msg.Data)
				//fmt.Println(gconv.String(msg.Data)),并且名称不能重复
				if dd == "111" && paiusers.Size() != 2 && !isRepeat(name) {
					//如果用户输入111,那么返回
					paiusers.Set(ws, name) //把用户加到组里面,如果人数满3人,就开始发牌,并且清空原来的数组
					if paiusers.Size() == 2 {
						//开始发牌
						if err = a.writeGroup1(); err != nil {
							g.Log().Error(err)
						}
						//a.ending()
					} else if err = a.writeGroup(
						model.ChatMsg{
							Type: "send",
							Data: ghtml.SpecialChars("当前人数" + gconv.String(paiusers.Size())),
							From: ghtml.SpecialChars(msg.From),
						}); err != nil {
						g.Log().Error(err)
					}

				} else if dd == "结果" || dd == "结束" {
					a.ending()
				} else if err = a.writeGroup(
					model.ChatMsg{
						Type: "send",
						Data: ghtml.SpecialChars(dd),
						From: ghtml.SpecialChars(msg.From),
					}); err != nil {
					g.Log().Error(err)
				}
			}
		}
	}
}

//判断用户是否重复输入111
func isRepeat(name string) (b bool) {
	paiusers.RLockFunc(func(m map[interface{}]interface{}) {
		for _, v := range m {
			if gconv.String(v) == name {
				b = true
			}
		}
	})
	return
}

//进入发牌
func (a *chatApi) writeGroup1() error {
	pai := paiinit(1) //拿到去掉大小王的牌
	//fmt.Println("基础的牌是", pai)
	var b []byte
	paiusers.RLockFunc(func(m map[interface{}]interface{}) {
		fmt.Println(m)
		for user, v := range m {

			name := gconv.String(v)
			uspai, newpai := fapai(pai)
			//把牌存个十分钟进缓存
			bo, _ := cache.Contains(CachePaiName + name)
			if bo {
				cache.Remove(CachePaiName + name)
			}
			num, _, _ := winAndLos(gconv.Strings(uspai))
			st := gconv.String(uspai)
			cache.Set("pai"+name, uspai, 1000*time.Minute)
			msg := model.ChatMsg{
				Type: "send",
				Data: st + niu(num),
				From: ghtml.SpecialChars("官方发牌员"),
			}
			b, _ = gjson.Encode(msg)
			pai = newpai
			user.(*ghttp.WebSocket).WriteMessage(ghttp.WS_MSG_TEXT, b)

		}
	})
	//paiusers.Clear()
	return nil
}

//计算有没有牛
func niu(num int8) string {

	str := ""
	switch num {
	case 0:
		str = "没有牛"
	case 10, 11, 12:
		str = "牛牛"
	default:
		str = fmt.Sprintf("牛%d", num)
	}
	return str
}

//获取发牌结果
func (a *chatApi) ending() (err error) {
	userpai := []UserPai{}
	maxu := UserPai{}
	res := "</br>" //双的牌
	paiusers.RLockFunc(func(m map[interface{}]interface{}) {
		//这里加一个定义谁输谁赢,赢多少倍的数据,

		//获取每个用户的点数
		for user, v := range m {
			fmt.Println(v)
			name := gconv.String(v)
			pai, _ := cache.Get(CachePaiName + name)             //获取缓存中的牌
			num, maxpai, maxnum := winAndLos(gconv.Strings(pai)) //获取牌中的点数,应该再加一个最大的牌
			doub := 1
			switch num {
			case 7, 8, 9:
				doub = 2
			case 10:
				doub = 3
			case 11:
				doub = 5
			}
			u := UserPai{
				Name:     name,
				Num:      num,
				Max:      maxpai,
				Multiple: int8(doub),
				MaxNum:   maxnum,
				Pai:      gconv.Strings(pai),
				User:     user,
			}
			fmt.Println("当前最大牌", maxpai, "点位是", maxnum)
			res += name + ":的牌是---" + strings.Join(gconv.Strings(pai), ",") + fmt.Sprintf("----为:%s", niu(num)) + "</br>"
			//获取胜负情况
			if num > maxu.Num || (num == maxu.Num && maxnum > maxu.MaxNum) {
				maxu = u
			}
			userpai = append(userpai, u)
		}
	})
	//开始把两个的牌情况整成数据发送出去
	for _, v := range userpai {
		str := res
		if maxu.Name == v.Name {
			str += fmt.Sprintf("</br>您赢了%d倍", maxu.Multiple)
		} else {
			str += fmt.Sprintf("</br>您输了%d倍", maxu.Multiple)
		}
		msg := model.ChatMsg{
			Type: "send",
			Data: str,
			From: ghtml.SpecialChars("官方发牌员"),
		}
		b, _ := gjson.Encode(msg)
		v.User.(*ghttp.WebSocket).WriteMessage(ghttp.WS_MSG_TEXT, b)
	}
	//开始计算谁输谁赢,正常只比点数,如果点数一样,那么比牌大小
	paiusers.Clear()
	return
}

//获取牌的点位与最大牌跟最大的点数
func winAndLos(pai []string) (int8, string, int) {
	//拿到具体的牌后,开始计算倍数与点数
	max := ""
	maxnum := 0
	jin := 0
	painum := []int{}
	num := int8(0)
	for _, v := range pai {
		d, dou := dian(v) //获取牌的点数与倍数,计算点与牌大小

		painum = append(painum, d)
		if dou > maxnum {
			maxnum = dou //计算最大的点位,
			max = v
		}
		if d > 10 {
			jin++
			d = 10
		}

		num += int8(d)
	}
	//五朵金花的
	if jin == 5 {
		num = 11
	} else {
		//开始正式计算点数
		num = godian(painum)
	}
	return num, max, maxnum
}

//开始计算是否为点数与牛牛
func godian(ints []int) int8 {
	var newints []int
	//五张牌,第一轮循环,先把花色去掉
	for _, v := range ints {
		if v < 10 {
			newints = append(newints, v)
		}
	}
	//把新的低于10个点的用算法计算出点数
	//如果剩下一张,那么直接返回一张的点

	//如果有三四五张怎么计算点数
	return gconv.Int8(word(newints))
}

func word(newints []int) int {
	le := len(newints)
	switch le {
	case 1:
		return newints[0]
	case 2:
		n := (newints[0] + newints[1]) % 10
		if n == 0 {
			return 10
		}
		return n
	case 3, 4, 5:
		//如果是有四五张,那么再加一个循环计算
		in := fourAndFive(newints)
		if len(in) > 2 {
			return 0
		}
		return word(in)
	}
	return 10
}

//把两两相加,三个相加为10的处理掉
func fourAndFive(ints []int) []int {
	//先检查一下有没有两两相加为十的
	le := len(ints)
	fmt.Printf("牌的长度为%d", le)
	for i := 0; i < le-2; i++ {
		for j := i + 1; j < le-1; j++ {
			for o := j + 1; o < le; o++ {
				n := ints[i] + ints[j] + ints[o]
				if n == 10 || n == 20 {
					ints = append(ints[:o], ints[o+1:]...)
					ints = append(ints[:j], ints[j+1:]...)
					ints = append(ints[:i], ints[i+1:]...)
					return ints
				}
			}
		}
	}
	for i := 0; i < le; i++ {
		for j := i + 1; j < le; j++ {
			if ints[i]+ints[j] == 10 {
				le -= 2
				ints = append(ints[:j], ints[j+1:]...)
				ints = append(ints[:i], ints[i+1:]...)
				i--
				break
			}
		}
	}

	return ints
}

//1-10为具体点数,11 12 13分别为jqk为花
func dian(s string) (d, dou int) {
	rs := []rune(s)
	switch string(rs[2:]) {
	case "A":
		d = 1
	case "J":
		d = 11
	case "Q":
		d = 12
	case "K":
		d = 13
	default:
		d = gconv.Int(string(rs[2:]))
	}

	switch string(rs[:2]) {
	case "黑桃":
		dou = 4
	case "红桃":
		dou = 3
	case "梅花":
		dou = 2
	case "方块":
		dou = 1
	}
	dou = dou + d*10
	return
}

//发五张牌,并且把剩下的牌返回回去,应该加一个如果剩余15张以后直接按序去发就好了.
func fapai(pai []string) (uspai []string, newpai []string) {
	//var uspai []string
	var num = 1
	for num <= 5 {
		//获取牌的长度,得到一个随机数
		i := ra(len(pai))
		num++
		uspai = append(uspai, pai[i])
		pai = append(pai[:i], pai[i+1:]...)
		//fmt.Println(num)
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
