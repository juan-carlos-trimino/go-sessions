package sessions

import (
  "fmt"
  "github.com/juan-carlos-trimino/go-logger"
  //The option -u instructs 'get' to update the module with dependencies.
  //go get -u github.com/google/uuid
  "github.com/google/uuid"
  //The option -u instructs 'get' to update the module with dependencies.
  //go get -u golang.org/x/crypto/bcrypt
  "golang.org/x/crypto/bcrypt"
  "net/http"
  "strings"
  "sync"
  "sync/atomic"
  "time"
)

const (
  falseCorrelationId = "-100"
)

var (
  //Keep the variables unexported (lowercase) so they can't be modified directly.
  sessionTimeout atomic.Int64
  //Grouping together three related variables in a single package-level variable, protect.
  shr = struct{  //Unnamed struct.
    /***
    It allows read-only operations to proceed in parallel with each other, but write operations to have fully exclusive access; this
    lock is called a multiple readers, single writer lock.

    It's only profitable to use an RWMutex when most of the goroutines that acquire the lock are readers, and the lock is under
    contention, that is, goroutines routinely have to wait to acquire it. An RWMutex requires more complex internal bookkeeping,
    making it slower than a regular mutex for uncontended locks.
    ***/
    session_lock sync.RWMutex  //Lock for the sessions map.
    //Store the session information for each user in memory.
    sessions map[string]session_token  //key: sessionToken, value: session
    //Store the username and password for each user.
    // users map[string][]byte //key: username, value: password
    // user_pwd sync.RWMutex  //Protect the map; embedded field.
    // file_user_pwd sync.Mutex  //Protect the file.
  }{
    sessions: make(map[string]session_token, 16),  //key: sessionToken, value: session
    //users: make(map[string][]byte, 16),
  }
)

type session_token struct{
  userName string
  expiry time.Time  //Enforce periodic session termination as a way to prevent session hijacking.
  csrfToken string
}

func init() {
  sessionTimeout.Store(int64(10 * time.Minute))
}

//Determine if a session has expired.
func IsSessionExpired(sessionToken string) bool {
  shr.session_lock.RLock()
  st, exists := shr.sessions[sessionToken]
  shr.session_lock.RUnlock()
  if exists {
    //If expiry is BEFORE the current time, it has expired.
    var expired bool = st.expiry.Before(time.Now())
    if expired {
      shr.session_lock.Lock()  //Writer.
      delete(shr.sessions, sessionToken)  //Delete the session.
      shr.session_lock.Unlock()
    }
    return expired
  }
  return !exists
}

func SessionExists(sessionToken string) bool {
  shr.session_lock.RLock()  //Readers lock.
  _, exists := shr.sessions[sessionToken]
  shr.session_lock.RUnlock()
  return exists
}

func CompareUuids(csrf, sessionToken string) bool {
  shr.session_lock.RLock()
  defer shr.session_lock.RUnlock()
  st, exists := shr.sessions[sessionToken]
  if exists {
    return strings.EqualFold(csrf, st.csrfToken)
  }
  return exists
}

func HashSecret(secret string) ([]byte, error) {
  hashedSecret, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
  return hashedSecret, err
}

func CompareHashAndPassword(hashedPassword []byte, password []byte) (bool, error) {
  err := bcrypt.CompareHashAndPassword(hashedPassword, password)
  return err == nil, err
}

func GetNewUuid() string {
  return uuid.NewString()
}

func CreateCookie(sessionToken string) (cookie *http.Cookie) {
  //https://en.wikipedia.org/wiki/HTTP_cookie
  //https://httpwg.org/specs/rfc6265.html
  cookie = &http.Cookie{
    Name: "session_token",
    Value: sessionToken,
    Path: "/",
    // Expires: sessions[sessionToken].Expiry,
    HttpOnly: true,
    SameSite: http.SameSiteStrictMode,
    Secure: true,
  }
  return
}

func AddEntryToSessions(userName string) (sessionToken string, session session_token) {
  /***
  Session based authentication keeps the users' sessions secure in a couple of ways:
  1. Since the session tokens are randomly generated, its near-impossible for a malicious user to brute-force his way into a
     user's session.
  2. If a user's session token is compromised somehow, it cannot be used after its expiry. This is why the expiry time is
     restricted to small intervals (a few seconds to a couple of minutes).
  ***/
  sessionToken = uuid.NewString()
  session = session_token{
    userName: userName,
    expiry: time.Now().Add(time.Duration(sessionTimeout.Load())),
    csrfToken: uuid.NewString(),
  }
  shr.session_lock.Lock()  //Writer.
  shr.sessions[sessionToken] = session
  shr.session_lock.Unlock()
  return
}

func UpdateEntryInSessions(oldSessionToken string) (newSessionToken string, session session_token) {
  newSessionToken = uuid.NewString()
  session = session_token{
    expiry: time.Now().Add(time.Duration(sessionTimeout.Load())),
    csrfToken: uuid.NewString(),
  }
  shr.session_lock.Lock()  //Writer.
  defer shr.session_lock.Unlock()
  //Fetch the existing session data and confirm it actually exists.
  oldSession, exists := shr.sessions[oldSessionToken]
  if !exists {
    return "", session_token{}  //Signal failure to the middleware.
  }
  session.userName = oldSession.userName  //Transfer the user name to the new session.
  delete(shr.sessions, oldSessionToken)
  shr.sessions[newSessionToken] = session
  return newSessionToken, session
}

func DeleteSession(sessionToken string) (cookie *http.Cookie) {
  shr.session_lock.Lock()  //Writer.
  delete(shr.sessions, sessionToken)
  shr.session_lock.Unlock()
  //Clear the browser cookie.
  cookie = &http.Cookie{
    Name: "session_token",
    Value: "",
    Path: "/",
    MaxAge: -1,
    HttpOnly: true,
    SameSite: http.SameSiteStrictMode,
    Secure: false,
  }
  return
}

func SetSessionTimeout(timeout time.Duration) {
  sessionTimeout.Store(int64(timeout))
}

func GetSessionTimeout() time.Duration {
  return time.Duration(sessionTimeout.Load())
}

func GetSessionTimeoutString() string {
  d := time.Duration(sessionTimeout.Load())
  return fmt.Sprintf("%02dh%02dm%02ds%05dms", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60, int(d.Milliseconds())%1000)
}

func GetUserName(sessionToken string) string {
  shr.session_lock.RLock()
  defer shr.session_lock.RUnlock()
  return shr.sessions[sessionToken].userName
}

func GetNumberOfSessions() int {
  shr.session_lock.RLock()
  defer shr.session_lock.RUnlock()
  return len(shr.sessions)
}

func (st *session_token) GetCsrfToken() string {
  return st.csrfToken
}

func (st *session_token) GetExpiry() time.Time {
  return st.expiry
}

func (st *session_token) GetUserName() string {
  return st.userName
}

/***
StartSessionSweeper kicks off a persistent background routine that deletes expired sessions from memory. Call this once
inside the main() function when starting the application.
***/
func StartSessionSweeper(timeout time.Duration) {
  //Fire and forget.
  go func(timeout time.Duration) {
    //Evaluate and purge the memory map every timeout value.
    ticker := time.NewTicker(timeout)
    //Keep looping in the background indefinitely.
    for range ticker.C {
      now := time.Now()
      var expiredTokens []string
      shr.session_lock.RLock()  //Read lock to scan and find expired tokens.
      for tokenKey, sessionItem := range shr.sessions {
        //If the session has officially crossed its expiration timeline.
        if now.After(sessionItem.expiry) {
          expiredTokens = append(expiredTokens, tokenKey)
        }
      }
      shr.session_lock.RUnlock()
      logger.LogInfo(fmt.Sprintf("Deleting %d session entries from the sessions map.", len(expiredTokens)), falseCorrelationId)
      //Write lock but only if there are actual items to delete.
      if len(expiredTokens) > 0 {
        shr.session_lock.Lock()
        for _, token := range expiredTokens {
          delete(shr.sessions, token)
        }
        shr.session_lock.Unlock()
      }
    }
  }(timeout)
  //StartSessionSweeper finishes instantly, but the goroutine keeps running.
}




func GetSessionTokens() (keys []string) {
  shr.session_lock.RLock()
  for key := range shr.sessions {
    keys = append(keys, key)
  }
  shr.session_lock.RUnlock()
  return keys
}
