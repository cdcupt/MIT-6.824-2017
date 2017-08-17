package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"math/rand"
	"sync"
	"time"
	"labrpc"
)

const None int = -1
const HeartBeatRate = 10
const ElectionTimeout = 150
const HeartTimeout = 5
//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make().
//
type ApplyMsg struct {
	Index       int
	Command     interface{}
	UseSnapshot bool   // ignore for lab2; only used in lab3
	Snapshot    []byte // ignore for lab2; only used in lab3
}

// Possible values for StateType.
const (
	StateFollower StateType = iota
	StateCandidate
	StateLeader
	StatePreCandidate
	numStates
)

// StateType represents the role of a node in a cluster.
type StateType uint

var stmap = [...]string{
	"StateFollower",
	"StateCandidate",
	"StateLeader",
	"StatePreCandidate",
}

func (st StateType) String() string {
	return stmap[int(st)]
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	state     StateType
	term      int                // current term
	leader    int                // leader peer's index
	// vote      int                // vote for
	votes     map[int]bool
	
	electionElapsed int
	heartbeatElapsed int
	//randomizedElectionTimeout is a random number between
	// [electiontimeout, 2 * electiontimeout - 1]. It gets reset
	// when raft changes its state to follower or candidate.
	randomTimeout int
	
	//tick func() // for heartbeat or election tick
	//step func(r *raft, m pb.Message) // for every role behavior

	committed int //index of highest log entry known to be committed
	applied int // index of highest log entry applied to state machine
	// only leader maintain 
	nextIndex []int // for each peer
  	matchIndex []int // 

	logEntries  []LogEntry
}

type LogEntry struct {
	Term int
	Value interface{}
	// Index int // or rather eid
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	term := rf.term
	isleader := false
	if rf.state == StateLeader {
		isleader = true
	}
	
	return term, isleader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := gob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := gob.NewDecoder(r)
	// d.Decode(&rf.xxx)
	// d.Decode(&rf.yyy)
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
}

func (rf *Raft) quorum() int { return len(rf.peers)/2 + 1 }

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	Term int // candidate’s term
	CandidateIndex int // candidate requesting vote
	LastLogIndex int // index of candidate’s last log entry
	LastLogTerm int // term of candidate's last log entry
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	Term int // currentTerm, for candidate to update itself
	VoteGranted bool // true means candidate received vote
}

//
// example RequestVote RPC handler.
// Invoked by candidates to gather votes
// Receiver behavior 
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	DPrintf("%x receive vote req %d at term %d", rf.me, args.CandidateIndex, args.Term)
	granted := false
	if args.Term > rf.term {
		// from leader
		granted = false
		rf.becomeFollower(args.Term)
	}

	if args.Term == rf.term {
		if rf.leader == None || rf.leader == args.CandidateIndex {
			lastLogTerm := rf.getLastLogTerm()
			lastLogIndex := rf.getLastLogIndex()
			if (args.LastLogTerm > lastLogTerm || (args.LastLogTerm == lastLogTerm && args.LastLogIndex >= lastLogIndex) ) {
				granted = true
				rf.leader = args.CandidateIndex
			}
		}
	}
	reply.VoteGranted = granted
	reply.Term = rf.term
}

type AppendEntriesArgs struct {
	Term int // current term
	Lead int // current leader id
	PrevLogIndex int // term of prevLogIndex entry
	PrevLogTerm int 
	LeaderCommit int
	Entries  []LogEntry
}

type AppendEntriesReply struct {
	Term int // for leader update
	Success bool
	MatchIndex int 
}

func min(x, y int) int {
	if x > y {
		return y
	}
	return x
}

func max(x, y int) int {
	if x > y {
		return x
	}
	return y
}

func (rf *Raft) getLastLogIndex() int {
	return len(rf.logEntries)
}

func (rf *Raft) getLastLogTerm() int {
	last := len(rf.logEntries)
	if last >= 1 {
		return rf.logEntries[last - 1].Term
	}
	return 0
}

// heartbeat and claim to be leader
//
func (rf * Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	DPrintf("%x receive append req %d at term %d", rf.me, args.Lead, args.Term)
	// heartbeat
	success := false
	matchIndex := 0
	if len(args.Entries) == 0 {
		rf.heartbeatElapsed = 0
	}
	// Receive from leader
	if args.Term > rf.term {
		rf.becomeFollower(args.Term)
	}

	if args.Term == rf.term {
		rf.becomeFollower(args.Term)

		if (rf.IsLogsContain(args.PrevLogTerm, args.PrevLogIndex)) {
			success = true
			// append logs ops
			index := args.PrevLogIndex
			lastIndex := rf.getLastLogIndex()
			start := 0
			for i, entry := range args.Entries {
				index += 1
				if index > lastIndex {
					break
				}
				// if any conflicts then delete from there
				if entry.Term != rf.logEntries[index].Term {
					rf.logEntries = rf.logEntries[index - 1:]
					start = i
					break
				}
				rf.logEntries = append(rf.logEntries, entry)
			}
			for i := start; i < len(args.Entries); i++ {
				rf.logEntries = append(rf.logEntries, args.Entries[i])
			}
			//rf.logEntries = append(rf.logEntries, args.Entries)
			matchIndex = index + len(args.Entries)
			if (args.LeaderCommit > rf.committed) {
				rf.committed = min(args.LeaderCommit, rf.getLastLogIndex())
			}
		}
		
	}

	reply.Term = rf.term
	reply.Success = success
	reply.MatchIndex = matchIndex
}


func (rf *Raft) IsLogsContain(index int, term int) bool {
	return index == 0 || index <= rf.getLastLogIndex() && rf.getLastLogTerm() == term
}

// when state changes
func (rf *Raft) reset(term int) {
	if rf.term != term {
		rf.term = term
		//rf.vote = None
	}
	rf.leader = None

	rf.electionElapsed = 0
	rf.heartbeatElapsed = 0
	rf.resetRandomizedElectionTimeout()

	//r.abortLeaderTransfer()

	rf.votes = make(map[int]bool)
}

func (rf *Raft) resetRandomizedElectionTimeout() {
	rf.randomTimeout = ElectionTimeout + rand.Intn(ElectionTimeout)
}

func (rf *Raft) becomeFollower(term int) {
	rf.state = StateFollower
	rf.reset(term)
	//rf.tick = tickElection
	DPrintf("%x became follower at term %d", rf.me, rf.term)
}

func (rf *Raft) becomeCandidate() {
	rf.state = StateCandidate
	rf.reset(rf.term + 1)
	//rf.tick = tickElection
	rf.leader = rf.me
	DPrintf("%x became candidate at term %d", rf.me, rf.term)
}

func (rf *Raft) becomeLeader() {
	if rf.state == StateFollower {
		panic("invalid transition [follower -> leader]")
	}
	rf.state = StateLeader
	rf.reset(rf.term)
	//rf.tick = tickHeartbeat
	rf.leader = rf.me
	DPrintf("%x became leader at term %d", rf.me, rf.term)
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	// DPrintf("%x send append req %d at term %d", rf.me, server, args.Term)
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := 0
	term, isLeader := rf.GetState()

	// Your code here (2B).
	// Raft phase two
	if isLeader {
		// update index and term
		// store log to local
		index = rf.nextIndex[rf.me] + 1
		rf.logEntries = append(rf.logEntries, LogEntry{rf.term, command})
		
		go rf.sendAllEntries()
	}
	
	return index, term, isLeader
}

func (rf *Raft) sendAllEntries() {
	for i := range rf.peers {
		if i == rf.me {
			continue
		}
		
		if rf.nextIndex[i] <= rf.getLastLogIndex() {
			preLogIndex := rf.nextIndex[i]
			preLogTerm := rf.logEntries[preLogIndex -1].Term
			sendLogs := rf.logEntries[preLogIndex:]

			req := AppendEntriesArgs{rf.term, rf.me, preLogIndex, preLogTerm, rf.committed, sendLogs}
			reply := AppendEntriesReply{}
			ok := rf.sendAppendEntries(i, &req, &reply)
			if ok {
				if reply.Term > rf.term {
					rf.becomeFollower(reply.Term)
				}
				if rf.state == StateLeader && rf.term == reply.Term {
					if (reply.Success){
						rf.matchIndex[i] = max(rf.matchIndex[i], reply.MatchIndex)
						rf.nextIndex[i] = reply.MatchIndex + 1;
					} else {
						rf.nextIndex[i] = max(1, rf.nextIndex[i] - 1);
					}
				}
			}
		}
	}
}	

//
// the tester calls Kill() when a Raft instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (rf *Raft) Kill() {
	// Your code here, if desired.
}

func (rf *Raft)sendPeerVote(index int) {
	// term could change
	term := rf.term
	candidateId := rf.me
	// 2b
	lastLogIndex := rf.getLastLogIndex()
	lastLogTerm := rf.getLastLogTerm() 

	req := RequestVoteArgs{term, candidateId, lastLogIndex, lastLogTerm}
	reply := RequestVoteReply{}
	ok := rf.sendRequestVote(index, &req, &reply)
	if ok {
		if reply.Term > rf.term {
			rf.becomeFollower(reply.Term)
		}

		if reply.Term == term  &&  rf.state == StateCandidate {
			rf.votes[index] = reply.VoteGranted
			// count
			rf.campaign()
		}
	}
}

func (rf *Raft)countVotes() (granted int) {
	for _, vv := range rf.votes {
		if vv {
			granted++
		}
	}
	return granted
}

func (rf *Raft)campaign() {
	//TODO: state could change
	// check if gather more than qurom number
	if rf.countVotes() == rf.quorum() {
		rf.becomeLeader()
		// broadcast win election
	}
}

// start in a goroutine
func (rf *Raft)startElection() {
	// gather all votes from peers
	for i, _ := range rf.peers {
		if i == rf.me {
			continue
		}
		
		go rf.sendPeerVote(i)
	}
}

func (rf *Raft)sendHeartbeat(index int) {
	preLogIndex := 0
	preLogTerm := 0

	sendLogs := make([]LogEntry,1,1)
	req := AppendEntriesArgs{rf.term, rf.me, preLogIndex, preLogTerm, rf.committed, sendLogs}
	reply := AppendEntriesReply{}
	rf.sendAppendEntries(index, &req, &reply)
	
	if reply.Term > rf.term {
		rf.becomeFollower(reply.Term)
	}
	//
}

// goroutine for heartbeat and election
func tickElection(rf *Raft) {
	// TODO: or every tick a goroutine?
	for {
		gap := time.Millisecond * HeartBeatRate
		tick := time.Tick(gap)
		select {
		case <- tick:

			if rf.state == StateLeader {
				// send heartbeat msg to all peers
				for i, _ := range rf.peers {
					if i != rf.me {
						go rf.sendHeartbeat(i)
					}
				}
				continue
			}
			// for candidate and follower
			rf.electionElapsed++
			rf.heartbeatElapsed++
			if rf.electionElapsed > rf.randomTimeout {
				DPrintf("%x discover election timeout at term %d", rf.me, rf.term)
				rf.electionElapsed = 0
				rf.becomeCandidate()
				go rf.startElection()
			}

			if rf.heartbeatElapsed >  HeartTimeout {
				DPrintf("%x discover heartbeat timeout at term %d", rf.me, rf.term)
				rf.heartbeatElapsed = 0
				rf.becomeCandidate()
				go rf.startElection()
			}
		}
	}
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int, persister *Persister, applyCh chan ApplyMsg) (*Raft) {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	rf.becomeFollower(0)
	// A thread periodically check leader state
	// if timeout issues RequestVote RPC to all other servers
	go tickElection(rf)

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())
	DPrintf("newRaft %x term: %d, commit: %d, applied: %d, lastindex: %d, lastterm: %d]",
		rf.me, rf.term, rf.committed, rf.applied, 0, 0)

	return rf
}
