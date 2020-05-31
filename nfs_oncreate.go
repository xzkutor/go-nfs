package nfs

import (
	"bytes"
	"context"

	"github.com/go-git/go-billy/v5"
	"github.com/vmware/go-nfs-client/nfs/xdr"
)

const (
	createModeUnchecked = 0
	createModeGuarded   = 1
	createModeExclusive = 2
)

func onCreate(ctx context.Context, w *response, userHandle Handler) error {
	obj := DirOpArg{}
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		// TODO: wrap
		return err
	}
	how, err := xdr.ReadUint32(w.req.Body)
	if err != nil {
		return err
	}
	var attrs *SetFileAttributes
	if how == createModeUnchecked || how == createModeGuarded {
		sattr, err := ReadSetFileAttributes(w.req.Body)
		if err != nil {
			return err
		}
		attrs = sattr
	} else if how == createModeExclusive {
		// read createverf3
		var verf [8]byte
		if err := xdr.Read(w.req.Body, verf); err != nil {
			return err
		}
		// TODO: support 'exclusive' mode.
		return &NFSStatusErrorWithWccData{NFSStatusNotSupp}
	} else {
		// invalid
		return &NFSStatusErrorWithWccData{NFSStatusNotSupp}
	}

	fs, path, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusStale}
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusErrorWithWccData{NFSStatusROFS}
	}

	if len(string(obj.Filename)) > PathNameMax {
		return &NFSStatusErrorWithWccData{NFSStatusNameTooLong}
	}

	newFilePath := fs.Join(append(path, string(obj.Filename))...)
	if s, err := fs.Stat(newFilePath); err == nil {
		if s.IsDir() {
			return &NFSStatusErrorWithWccData{NFSStatusExist}
		}
		if how == createModeGuarded {
			return &NFSStatusErrorWithWccData{NFSStatusExist}
		}
	} else {
		if s, err := fs.Stat(fs.Join(path...)); err != nil {
			return &NFSStatusErrorWithWccData{NFSStatusAccess}
		} else if !s.IsDir() {
			return &NFSStatusErrorWithWccData{NFSStatusNotDir}
		}
	}

	file, err := fs.Create(newFilePath)
	if err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusAccess}
	}
	if err := file.Close(); err != nil {
		return &NFSStatusErrorWithWccData{NFSStatusAccess}
	}

	fp := userHandle.ToHandle(fs, append(path, file.Name()))
	changer := userHandle.Change(fs)
	if changer != nil {
		if err := attrs.Apply(changer, fs, newFilePath); err != nil {
			return &NFSStatusErrorWithWccData{NFSStatusIO}
		}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return err
	}

	if err := xdr.Write(writer, fp); err != nil {
		return err
	}
	WritePostOpAttrs(writer, fs, append(path, file.Name()))

	// dir_wcc (we don't include pre_op_attr)
	if err := xdr.Write(writer, uint32(0)); err != nil {
		return err
	}
	WritePostOpAttrs(writer, fs, path)

	return w.Write(writer.Bytes())
}