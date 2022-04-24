package rpcdemo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"rpcdemo/codec"
	"sync"
)

// 实施一个rpc调用所用信息
type Call struct {
	Seq           uint64
	ServiceMethod string      // <service>.<method>
	Args          interface{} // rpc调用参数
	Reply         interface{} // 返回值
	Error         error       // 调用可能会有的错误
	Done          chan *Call  // 当调用结束时通知调用方
}

func (call *Call) done() {
	call.Done <- call
}

//rpc客户端、可能会被多个协程同时使用
type Client struct {
	cc       codec.Codec //编解码器
	opt      *Option     //消息编码方式
	sending  sync.Mutex  //保证请求有序发送  防止出现多个请求报文混淆
	header   codec.Header
	mu       sync.Mutex // 互斥访问client
	seq      uint64
	pending  map[uint64]*Call // 等待调用集合
	closing  bool             // 用户主动关闭
	shutdown bool             // 服务器出错
}

var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("connection is shut down")

//关闭连接
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing {
		return ErrShutdown
	}
	client.closing = true
	return client.cc.Close()
}

//查看客户端是否可用
func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return !client.shutdown && !client.closing
}

//删除请求
func (client *Client) removeCall(seq uint64) *Call {
	client.mu.Lock()
	defer client.mu.Unlock()
	call := client.pending[seq]
	delete(client.pending, seq)
	return call
}

//终止所有正在等待的请求的call
func (client *Client) terminateCalls(err error) {
	client.sending.Lock()
	defer client.sending.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	client.shutdown = true
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
}

//发送请求
func (client *Client) send(call *Call) {
	// 确保请求有序发送
	client.sending.Lock()
	defer client.sending.Unlock()

	// 注册call
	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}

	// 请求头
	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = ""

	// 加密并发送请求
	if err := client.cc.Write(&client.header, call.Args); err != nil {
		//请求发送失败的处理
		call := client.removeCall(seq)
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

//注册请求
func (client *Client) registerCall(call *Call) (uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}
	call.Seq = client.seq
	client.pending[call.Seq] = call
	client.seq++
	return call.Seq, nil
}

//接收响应
func (client *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header
		if err = client.cc.ReadHeader(&h); err != nil {
			break
		}
		//处理完毕 删除调用
		call := client.removeCall(h.Seq)
		switch {
		case call == nil:
			// 请求部分错误或者已经删除该调用
			err = client.cc.ReadBody(nil)
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done()
		default:
			//读取响应
			err = client.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body " + err.Error())
			}
			call.done()
		}
	}
	// 错误发生 结束所有调用
	client.terminateCalls(err)
}

//提供给用户的rpc调用接口 异步调用
func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("rpc client: done channel is unbuffered")
	}
	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	client.send(call)
	return call
}

// 提供给用户的rpc调用接口 同步调用
func (client *Client) Call(serviceMethod string, args, reply interface{}) error {
	//同步调用 阻塞call.done 等待响应返回
	call := <-client.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}

// 便于用户开启服务器地址，创建client实例  option为可选参数
func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	// 如果有误则关闭连接
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()
	return NewClient(conn, opt)
}

//初始化客户端 完成协议交换协商好消息编码方式
func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}
	//  告知服务器信息编码方式
	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: options error: ", err)
		_ = conn.Close()
		return nil, err
	}
	return newClientCodec(f(conn), opt), nil
}

func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		seq:     1, // seq starts with 1, 0 means invalid call
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	//开启新协程接收响应
	go client.receive()
	return client
}

func parseOptions(opts ...*Option) (*Option, error) {
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}
	opt := opts[0]
	opt.MagicNumber = DefaultOption.MagicNumber
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType
	}
	return opt, nil
}
