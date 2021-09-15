package beaconeye

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"gBeaconEye/win32"
	"os"
	"strings"
	"syscall"
	"unsafe"

	gops "github.com/mitchellh/go-ps"
)

const ARBITRARY = 0x100
const NOP = -1

type MatchResult struct {
	Addr uint64
}

type EvilResult struct {
	Arch string
	Path string
	Addr uint64
}

type ProcessScan struct {
	// the handle of the process
	Handle win32.HANDLE
	// Wow64Info
	// If this value is nonzero, the process is running in a WOW64 environment;
	// otherwise, if the value is equal to zero, the process is not running in a WOW64 environment.
	Wow64Info uintptr
	// Is64Bit whether the process is 64 bit process
	Is64Bit bool
	// PebAddress the base address of peb of the process
	PebAddress uintptr
	// NumberOfHeaps Number of the processes
	NumberOfHeaps uint32
	// ProcHeapsArrayAddr is the first address of a heap array, and each array element is pointer which pointer to heap.
	ProcHeapsArrayAddr uintptr
	// heaps is the array, and each array element is pointer which pointer to heap.
	Heaps []uintptr
}

func NewProcessScan(pid win32.DWORD) (processScan *ProcessScan, err error) {
	processScan = new(ProcessScan)
	// get handle of process
	processScan.Handle = win32.OpenProcess(win32.PROCESS_ALL_ACCESS, win32.FALSE, pid)
	if processScan.Handle == 0 {
		err = fmt.Errorf("cannot OpenProcess with pid %d", pid)
		return
	}
	processScan.Wow64Info, err = GetProcWow64Info(processScan.Handle)
	if err != nil {
		return
	}
	processScan.Is64Bit = processScan.Wow64Info == 0
	// get peb base address of process
	processScan.PebAddress, err = GetProcPebAddr(processScan.Handle)
	if err != nil {
		return
	}
	// get heap num and heap array address and all heaps of process
	err = processScan.initHeapsInfo()
	return
}

func (p *ProcessScan) initHeapsInfo() (err error) {
	var numHeapsAddr uintptr
	var heapArrayAddr uintptr

	pebBaseAddr := p.PebAddress
	if p.Is64Bit {
		numHeapsAddr = pebBaseAddr + uintptr(0xE8)
		heapArrayAddr = pebBaseAddr + uintptr(0xF0)
	} else {
		numHeapsAddr = pebBaseAddr + uintptr(0x88)
		heapArrayAddr = pebBaseAddr + uintptr(0x90)
	}
	p.NumberOfHeaps, err = GetProcUint32(p.Handle, numHeapsAddr)
	// fmt.Printf("debug: numHeaps: %x\n", p.NumberOfHeaps)
	if err != nil {
		return
	}
	p.ProcHeapsArrayAddr, err = GetProcUintptr(p.Handle, heapArrayAddr, p.Is64Bit)
	// fmt.Printf("debug: heapArray: %x\n", p.ProcHeapsArrayAddr)
	if err != nil {
		return
	}

	for idx := 0; uint32(idx) < p.NumberOfHeaps; idx++ {
		var len_ int
		var heap uintptr
		if p.Is64Bit {
			len_ = 8
		} else {
			len_ = 4
		}
		heap, err = GetProcUintptr(p.Handle, p.ProcHeapsArrayAddr+uintptr(idx*len_), p.Is64Bit)
		if err != nil {
			return
		}
		p.Heaps = append(p.Heaps, heap)
	}
	return nil
}

func (p *ProcessScan) SearchMemory(matchStr string, pResultArray *[]MatchResult) (err error) {
	matchArray, err := GetMatchArray(matchStr)
	if err != nil {
		return err
	}
	next := GetNext(matchArray)
	for _, heap := range p.Heaps {
		// fmt.Printf("debug: heap: %x\n", heap)
		mbi, err := win32.VirtualQueryEx(p.Handle, win32.LPCVOID(heap))
		if err != nil {
			break
		}
		if err = SearchMemoryBlock(p.Handle, matchArray, uint64(mbi.BaseAddress), int64(mbi.RegionSize), next, pResultArray); err != nil {
			return err
		}
	}
	return nil
}

func FindEvil() (evilResults []EvilResult, err error) {
	var processes []gops.Process
	processes, err = GetProcesses()
	if err != nil {
		return
	}
	rule64 := "00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 01 00 00 00 00 00 00 00 ?? 00 00 00 00 00 00 00 01 00 00 00 00 00 00 00 ?? ?? 00 00 00 00 00 00 02 00 00 00 00 00 00 00 ?? ?? ?? ?? 00 00 00 00 02 00 00 00 00 00 00 00 ?? ?? ?? ?? 00 00 00 00 01 00 00 00 00 00 00 00 ?? ?? 00 00 00 00 00 00"
	rule32 := "00 00 00 00 00 00 00 00 01 00 00 00 ?? 00 00 00 01 00 00 00 ?? ?? 00 00 02 00 00 00 ?? ?? ?? ?? 02 00 00 00 ?? ?? ?? ?? 01 00 00 00 ?? ?? 00 00"
	fmt.Printf("processes len %d\n", len(processes))
	for _, process := range processes {
		// 如果是当前运行进程则跳过
		if os.Getpid() == process.Pid() {
			continue
		}
		// fmt.Printf("debug: Start scan process %d:%s\n", process.Pid(), process.Executable())
		processScan, err := NewProcessScan(win32.DWORD(process.Pid()))
		if err != nil {
			fmt.Printf("init process info error: %v\n", err)
			continue
		}
		rule := rule32
		if processScan.Is64Bit {
			rule = rule64
		}
		var resultArray []MatchResult
		err = processScan.SearchMemory(rule, &resultArray)
		if err != nil {
			fmt.Printf("SearchMemory error: %v\n", err)
			continue
		}
		if len(resultArray) != 0 {
			fmt.Printf("find evil: %s\n", process.Executable())
		}
		// searchEvil(handle, "4C 8B 53 08 45 8B 0A 45 8B 5A 04 4D 8D 52 08 45 85 C9 75 05 45 85 DB 74 33 45 3B CB 73 E6 49 8B F9 4C 8B 03", 0x410000, 0xFFFFFFFF, &evilResults, process, "x64-1")
		// searchEvil(handle, "8B 46 04 8B 08 8B 50 04 83 C0 08 89 55 08 89 45 0C 85 C9 75 04 85 D2 74 23 3B CA 73 E6 8B 06 8D 3C 08 33 D2", 0x410000, 0xFFFFFFFF, &evilResults, process, "x86-2")
	}
	return
}

func SearchMemoryBlock(hProcess win32.HANDLE, matchArray []uint16, startAddr uint64, size int64, next []int16, pResultArray *[]MatchResult) (err error) {
	var memBuf []byte
	memBuf, err = win32.NtReadVirtualMemory(hProcess, win32.PVOID(startAddr), size)
	if err != nil {
		err = fmt.Errorf("%v: %v", err, syscall.GetLastError())
		return
	}

	// sunday algorithm implement
	i := 0      // 父串index
	j := 0      // 字串index
	offset := 0 // 下次匹配的偏移（基于起始位置0）

	for int64(offset) < size {
		// 将父串index设置到偏移量，字串index设置到0
		i = offset
		j = 0
		// 判断匹配
		for j < len(matchArray) && int64(i) < size {
			if matchArray[j] == uint16(memBuf[i]) || int(matchArray[j]) == ARBITRARY {
				i++
				j++
			} else {
				break
			}
		}

		// 如果一直到最后一位，则代表匹配成功
		if j == len(matchArray) {
			*pResultArray = append(*pResultArray, MatchResult{
				Addr: startAddr + uint64(offset),
			})
		}

		// 移至子串在父串对应位置的末尾，如果超出长度则没有匹配到
		if int64(offset+len(matchArray)) >= size {
			return
		}

		// 获取父串中字串末尾所在位置字符，将子串中和该位置相等的字符对齐
		// 得出字串需要移动多少位
		valueAtMIdx := memBuf[offset+len(matchArray)]
		idxInSub := next[valueAtMIdx]
		if idxInSub == NOP { // 可能是匹配不到，或者可以匹配到 ?? 符号
			offset = offset + (len(matchArray) - int(next[ARBITRARY])) // 如果字串存在 ?? 号，则下次匹配移动到该位置开始匹配，否则移动到末尾，即 m = m + 字串长度 + 1
		} else {
			offset = offset + (len(matchArray) - int(idxInSub))
		}
	}
	return
}

// GetMatchArray get []uint16 from string
func GetMatchArray(matchStr string) ([]uint16, error) {
	codes := strings.Split(matchStr, " ")
	result := make([]uint16, len(codes))
	for i, c := range codes {
		if c == "??" {
			result[i] = ARBITRARY
		} else {
			bs, err := hex.DecodeString(c)
			if err != nil {
				return nil, err
			}
			result[i] = uint16(bs[0])
		}
	}
	return result, nil
}

func GetNext(matchArray []uint16) []int16 {
	//特征码（字节集）的每个字节的范围在0-255（0-FF）之间，256用来表示问号，到260是为了防止越界
	next := make([]int16, 260)
	for i := 0; i < len(next); i++ {
		next[i] = NOP
	}
	for i := 0; i < len(matchArray); i++ {
		next[matchArray[i]] = int16(i)
	}
	return next
}

func GetProcPebAddr(hProcess win32.HANDLE) (PebAddress uintptr, err error) {
	var basicInfo win32.PROCESS_BASIC_INFORMATION
	var retLen win32.ULONG
	var wow64Ret uintptr
	wow64Ret, err = GetProcWow64Info(hProcess)
	if err != nil {
		return
	}
	if wow64Ret != 0 {
		PebAddress = wow64Ret
		return
	}
	_, err = win32.NtQueryInformationProcess(
		hProcess,
		win32.ProcessBasicInformation,
		unsafe.Pointer(&basicInfo),
		win32.ULONG(win32.SizeOfProcessBasicInformation),
		&retLen,
	)
	if err != nil {
		err = fmt.Errorf("get peb addr error: %v", err)
		return
	}
	PebAddress = basicInfo.PebBaseAddress
	return
}

func GetProcUint32(hProcess win32.HANDLE, addr uintptr) (num uint32, err error) {
	var numBytes []byte
	numBytes, err = win32.NtReadVirtualMemory(hProcess, win32.PVOID(addr), 4)
	if err != nil {
		return
	}
	num = binary.LittleEndian.Uint32(numBytes)
	return
}

func GetProcUintptr(hProcess win32.HANDLE, addr uintptr, is64Bit bool) (ptr uintptr, err error) {
	if is64Bit {
		var ptr_ []byte
		ptr_, err = win32.NtReadVirtualMemory(hProcess, win32.PVOID(addr), 8)
		if err != nil {
			return
		}
		ptr = uintptr(binary.LittleEndian.Uint64(ptr_))
	} else {
		var ptr_ []byte
		ptr_, err = win32.NtReadVirtualMemory(hProcess, win32.PVOID(addr), 4)
		if err != nil {
			return
		}
		ptr = uintptr(binary.LittleEndian.Uint32(ptr_))
	}
	return
}

func GetProcWow64Info(hProcess win32.HANDLE) (ret uintptr, err error) {
	var ulongWow64 win32.ULONG
	var retLen win32.ULONG
	_, err = win32.NtQueryInformationProcess(
		hProcess,
		win32.ProcessWow64Information,
		unsafe.Pointer(&ulongWow64),
		win32.ULONG(unsafe.Sizeof(ulongWow64)),
		&retLen,
	)
	if err != nil {
		err = fmt.Errorf("get isWow64 error: %v", err)
		return
	}
	ret = uintptr(ulongWow64)
	return
}