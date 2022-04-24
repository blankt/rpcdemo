package rpcdemo

import (
	"go/ast"
	"log"
	"reflect"
	"sync/atomic"
)

//方法类型
type methodType struct {
	method    reflect.Method
	ArgType   reflect.Type //第一个参数类型
	ReplyType reflect.Type //第二个参数类型
	numCalls  uint64       //方法调用次数
}

func (m *methodType) NumCalls() uint64 {
	return atomic.LoadUint64(&m.numCalls)
}

func (m *methodType) newArgv() reflect.Value {
	var argv reflect.Value
	//type或许为值类型或者为指针类型
	if m.ArgType.Kind() == reflect.Ptr {
		argv = reflect.New(m.ArgType.Elem()) //返回一个value类型值，该值持有一个指向类型为参数的新申请的零值指针
	} else {
		argv = reflect.New(m.ArgType).Elem()
	}
	return argv
}

func (m *methodType) newReplyv() reflect.Value {
	replyv := reflect.New(m.ReplyType.Elem())
	switch m.ReplyType.Kind() {
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(m.ReplyType.Elem()))
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(m.ReplyType.Elem(), 0, 0))
	}
	return replyv
}

//将一个类型结构体映射到服务
type service struct {
	name   string                 //映射的结构体的名称
	typ    reflect.Type           //结构体类型
	rcvr   reflect.Value          // 结构体实例本身
	method map[string]*methodType //存储映射的结构体和所有符合条件的方法
}

//通过反射调用方法
func (s *service) call(m *methodType, argv, replyv reflect.Value) error {
	atomic.AddUint64(&m.numCalls, 1)
	f := m.method.Func
	returnValues := f.Call([]reflect.Value{s.rcvr, argv, replyv}) //调用f函数 参数为s.rcvr, argv, replyv  反射调用时第0个参数为调用者自己
	if errInter := returnValues[0].Interface(); errInter != nil {
		return errInter.(error)
	}
	return nil
}

func newService(rcvr interface{}) *service {
	s := new(service)
	s.rcvr = reflect.ValueOf(rcvr) //返回接口rcvr保存的具体值
	s.name = reflect.Indirect(s.rcvr).Type().Name()
	s.typ = reflect.TypeOf(rcvr)
	if !ast.IsExported(s.name) { //结构体名称是否以大写字母开头
		log.Fatalf("rpc server: %s is not a valid service name", s.name)
	}
	s.registerMethods()
	return s
}

//注册方法
func (s *service) registerMethods() {
	s.method = make(map[string]*methodType)
	for i := 0; i < s.typ.NumMethod(); i++ { //遍历类型中的方法
		method := s.typ.Method(i)
		mType := method.Type
		if mType.NumIn() != 3 || mType.NumOut() != 1 { //方法的入参不为3或返回值不为1
			continue
		}
		if mType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() { //如果返回值不为error
			continue
		}
		//方法的两个参数
		argType, replyType := mType.In(1), mType.In(2)
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}
		//方法加入该服务中
		s.method[method.Name] = &methodType{
			method:    method,
			ArgType:   argType,
			ReplyType: replyType,
		}
		log.Printf("rpc server: register %s.%s\n", s.name, method.Name)
	}
}

//判断方法是否是以大写字母开头和包路径是否为""
func isExportedOrBuiltinType(t reflect.Type) bool {
	return ast.IsExported(t.Name()) || t.PkgPath() == ""
}
