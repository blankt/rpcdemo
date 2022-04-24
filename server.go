package rpcdemo

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"reflect"
	"rpcdemo/codec"
	"strings"
	"sync"
)

const MagicNumber = 0x3bef5c

type Option struct {
	MagicNumber int        // 标识为一个rpc请求
	CodecType   codec.Type // 客户端选择不同的方式编码传递的信息
}

var DefaultOption = &Option{
	MagicNumber: MagicNumber,
	CodecType:   codec.GobType,
}

type Server struct {
	serviceMap sync.Map
}

func NewServer() *Server {
	return &Server{}
}

//默认server实例
var DefaultServer = NewServer()

// 处理连接
func (server *Server) ServeConn(conn io.ReadWriteCloser) {
	defer func() { _ = conn.Close() }()
	//获得option实例
	var opt Option
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server: options error: ", err)
		return
	}
	//看是否为一个rpc请求
	if opt.MagicNumber != MagicNumber {
		log.Printf("rpc server: invalid magic number %x", opt.MagicNumber)
		return
	}
	//得到对应消息解码器
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Printf("rpc server: invalid codec type %s", opt.CodecType)
		return
	}

	server.serveCodec(f(conn)) //f(conn) 得到对应消息编解码实例
}

var invalidRequest = struct{}{}

// 三步： 1、读取请求 2、处理请求 3、回复请求
func (server *Server) serveCodec(cc codec.Codec) {
	sending := new(sync.Mutex) // 加锁确保回复逐个发送
	wg := new(sync.WaitGroup)  // 确保处理所有请求
	for {
		req, err := server.readRequest(cc)
		if err != nil {
			if req == nil {
				break // 解析请求失败时,结束循环
			}
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		//并发处理请求
		go server.handleRequest(cc, req, sending, wg)
	}
	wg.Wait()
	_ = cc.Close()
}

// 存储请求信息
type request struct {
	h            *codec.Header // 请求头
	argv, replyv reflect.Value // 参数和返回值
	mtype        *methodType
	svc          *service
}

func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHeader(&h); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error:", err)
		}
		return nil, err
	}
	return &h, nil
}

func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}

	req.svc, req.mtype, err = server.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	//确定请求中参数和返回值的类型
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()

	argvi := req.argv.Interface()
	if req.argv.Type().Kind() != reflect.Ptr {
		argvi = req.argv.Addr().Interface()
	}
	// 报文反序列化为第一个入参 读取请求body中的参数到argvi
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server : read body err:", err)
		return req, nil
	}
	return req, nil
}

func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error:", err)
	}
}

func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	defer wg.Done()
	err := req.svc.call(req.mtype, req.argv, req.replyv)

	if err != nil {
		req.h.Error = err.Error()
		server.sendResponse(cc, req.h, invalidRequest, sending)
		return
	}
	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
}

// 服务器接收连接 并开启协程处理连接
func (server *Server) Accept(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Println("rpc server: accept error:", err)
			return
		}
		go server.ServeConn(conn)
	}
}

// 用defaultserver处理连接
func Accept(lis net.Listener) { DefaultServer.Accept(lis) }

func Register(rcvr interface{}) error {
	return DefaultServer.Register(rcvr)
}

//注册服务
func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc : service already defined:" + s.name)
	}
	return nil
}

//找到服务
func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".") //判断serviceMethod是否合法
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	svci, ok := server.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server: can't find service " + serviceName)
		return
	}
	svc = svci.(*service)
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method " + methodName)
	}
	return
}
