package sessions

import (
  "sync"
)

//Grouping together three related variables in a single package-level variable, protect.
var shr1 = struct{  //Unnamed struct.
  session_lock sync.RWMutex  //Lock for the sessions map.
  //Store the session information for each user in memory.
  sessions map[string]session_token  //key: sessionToken, value: session
  mu sync.Mutex  //Protect the file.
  /***
  It allows read-only operations to proceed in parallel with each other, but write operations to have fully exclusive access; this
  lock is called a multiple readers, single writer lock.

  It's only profitable to use an RWMutex when most of the goroutines that acquire the lock are readers, and the lock is under
  contention, that is, goroutines routinely have to wait to acquire it. An RWMutex requires more complex internal bookkeeping,
  making it slower than a regular mutex for uncontended locks.
  ***/
  sync.RWMutex  //Protect the map; embedded field.
  //Using pointer and non-pointer (see main.go [h.mux = make(map[string]http.HandlerFunc, 128)])
  //Store the username and password for each user.
  // users map[string]*[]byte  //key: username, value: password
//users map[string][]byte //key: username, value: password
}{
  // users: make(map[string]*[]byte, 16),
//users: make(map[string][]byte, 16),
  sessions: make(map[string]session_token, 16),  //key: sessionToken, value: session
}
