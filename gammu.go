// Go binding for libGammu (library to work with different cell phones)
package gammu

/*
#include <gammu.h>

void sendCallback(GSM_StateMachine *sm, int status, int msgRef, void *data) {
	if (status==0) {
		*((GSM_Error *) data) = ERR_NONE;
	} else {
		*((GSM_Error *) data) = ERR_UNKNOWN;
	}
}
void setStatusCallback(GSM_StateMachine *sm, GSM_Error *status) {
	GSM_SetSendSMSStatusCallback(sm, sendCallback, status);
}
GSM_Debug_Info *debug_info;
void setDebug() {
	debug_info = GSM_GetGlobalDebug();
	GSM_SetDebugFileDescriptor(stderr, TRUE, debug_info);
	GSM_SetDebugLevel("textall", debug_info);
}

#cgo pkg-config: gammu
*/
import "C"
import (
	"time"
	"unsafe"
)

// Error
type Error C.GSM_Error

func (e Error) Error() string {
	return C.GoString(C.GSM_ErrorString(C.GSM_Error(e)))
}

// StateMachine
type StateMachine struct {
	g      *C.GSM_StateMachine
	smsc   C.GSM_SMSC
	status C.GSM_Error

	Timeout time.Duration // Default 10s
}

// Creates new state maschine using cf configuration file or default
// configuration file if cf == "".
func NewStateMachine(cf string) (*StateMachine, error) {
	//C.setDebug()
	var sm StateMachine

	var config *C.INI_Section
	if cf != "" {
		cs := C.CString(cf)
		defer C.free(unsafe.Pointer(cs))
		if e := C.GSM_FindGammuRC(&config, cs); e != C.ERR_NONE {
			return nil, Error(e)
		}
	} else {
		if e := C.GSM_FindGammuRC(&config, nil); e != C.ERR_NONE {
			return nil, Error(e)
		}
	}
	defer C.INI_Free(config)

	sm.g = C.GSM_AllocStateMachine()
	if sm.g == nil {
		panic("out of memory")
	}

	if e := C.GSM_ReadConfig(config, C.GSM_GetConfig(sm.g, 0), 0); e != C.ERR_NONE {
		sm.Free()
		return nil, Error(e)
	}
	C.GSM_SetConfigNum(sm.g, 1)
	sm.Timeout = 10 * time.Second

	return &sm, nil
}

func (sm *StateMachine) Free() {
	C.GSM_FreeStateMachine(sm.g)
	sm.g = nil
}

func (sm *StateMachine) Connect() error {
	if e := C.GSM_InitConnection(sm.g, 1); e != C.ERR_NONE {
		return Error(e)
	}
	C.setStatusCallback(sm.g, &sm.status)
	sm.smsc.Location = 1
	if e := C.GSM_GetSMSC(sm.g, &sm.smsc); e != C.ERR_NONE {
		return Error(e)
	}
	return nil
}

func (sm *StateMachine) Disconnect() error {
	if e := C.GSM_TerminateConnection(sm.g); e != C.ERR_NONE {
		return Error(e)
	}
	return nil
}

func decodeUTF8(out *C.uchar, in string) {
	cn := C.CString(in)
	C.DecodeUTF8(out, cn, C.int(len(in)))
	C.free(unsafe.Pointer(cn))
}

func (sm *StateMachine) sendSMS(sms *C.GSM_SMSMessage) error {
	C.CopyUnicodeString(&sms.SMSC.Number[0], &sm.smsc.Number[0])
	// Send mepssage
	sm.status = C.ERR_TIMEOUT
	if e := C.GSM_SendSMS(sm.g, sms); e != C.ERR_NONE {
		return Error(e)
	}
	// Wait for reply
	t := time.Now()
	for time.Now().Sub(t) < sm.Timeout {
		C.GSM_ReadDevice(sm.g, C.TRUE)
		if sm.status == C.ERR_NONE {
			// Message sent OK
			break
		} else if sm.status != C.ERR_TIMEOUT {
			// Error
			break
		}
	}
	if sm.status != C.ERR_NONE {
		return Error(sm.status)
	}
	return nil
}

func (sm *StateMachine) SendSMS(number, text string) error {
	var sms C.GSM_SMSMessage
	decodeUTF8(&sms.Number[0], number)
	decodeUTF8(&sms.Text[0], text)
	sms.PDU = C.SMS_Submit
	sms.UDH.Type = C.UDH_NoUDH
	sms.Coding = C.SMS_Coding_Default_No_Compression
	sms.Class = 1
	return sm.sendSMS(&sms)
}

func (sm *StateMachine) SendLongSMS(number, text string) error {
	// Fill in SMS info
	var smsInfo C.GSM_MultiPartSMSInfo
	C.GSM_ClearMultiPartSMSInfo(&smsInfo)
	smsInfo.Class = 1
	smsInfo.EntriesNum = 1
	smsInfo.UnicodeCoding = C.FALSE
	// Check for non-ASCII rune
	for _, r := range text {
		if r > 0x7F {
			smsInfo.UnicodeCoding = C.TRUE
			break
		}
	}
	smsInfo.Entries[0].ID = C.SMS_ConcatenatedTextLong
	msgUnicode := make([]C.uchar, (len(text)+1)*2)
	decodeUTF8(&msgUnicode[0], text)
	smsInfo.Entries[0].Buffer = &msgUnicode[0]
	// Prepare multipart message
	var msms C.GSM_MultiSMSMessage
	if e := C.GSM_EncodeMultiPartSMS(nil, &smsInfo, &msms); e != C.ERR_NONE {
		return Error(e)
	}
	// Send message

	for i := 0; i < int(msms.Number); i++ {
		sms := msms.SMS[i]
		decodeUTF8(&sms.Number[0], number)
		sms.PDU = C.SMS_Submit
		if e := sm.sendSMS(&sms); e != nil {
			return e
		}
	}
	return nil
}