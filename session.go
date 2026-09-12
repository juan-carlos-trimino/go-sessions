package sessions

import (
  "fmt"
  //The option -u instructs 'get' to update the module with dependencies.
  //go get -u github.com/google/uuid
  "github.com/google/uuid"
  //The option -u instructs 'get' to update the module with dependencies.
  //go get -u golang.org/x/crypto/bcrypt
  "golang.org/x/crypto/bcrypt"
  "net/http"
  "strings"
  "sync/atomic"
  "time"
)

var (
  //Keep the variables unexported (lowercase) so they can't be modified directly.
  sessionTimeout atomic.Int64
)

func init() {
  sessionTimeout.Store(int64(10 * time.Minute))
}

type session_token struct{
  userName string
  expiry time.Time  //Enforce periodic session termination as a way to prevent session hijacking.
  csrfToken string
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

func HashSecret(secret string) ([]byte, error) {
  hashedSecret, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
  return hashedSecret, err
}

func CompareHashAndPassword(hashedPassword[]byte, password []byte) (bool, error) {
  err := bcrypt.CompareHashAndPassword(hashedPassword, password)
  return err == nil, err
}

func CompareUuids(csrf, sessionToken string) bool {
  shr.session_lock.RLock()
  st, exists := shr.sessions[sessionToken]
  shr.session_lock.RUnlock()
  if exists {
    return strings.EqualFold(csrf, st.csrfToken)
  }
  return exists
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
    Secure: false,
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
  session.userName = shr.sessions[oldSessionToken].userName
  delete(shr.sessions, oldSessionToken)
  shr.sessions[newSessionToken] = session
  return
}

func DeleteSession(sessionToken string) (cookie *http.Cookie) {
  shr.session_lock.Lock()  //Writer.
  delete(shr.sessions, sessionToken)
  shr.session_lock.Unlock()
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

func GetNewUuid() string {
  return uuid.NewString()
}

func (st *session_token) GetCsrfToken() string {
  return st.csrfToken
}
