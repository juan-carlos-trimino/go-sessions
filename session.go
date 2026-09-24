package sessions

import (
  "context"
  "errors"
  "fmt"
  "github.com/juan-carlos-trimino/go-logger"
  //The option -u instructs 'get' to update the module with dependencies.
  //go get -u github.com/google/uuid
  "github.com/google/uuid"
  //go get github.com/redis/go-redis/v9
  "github.com/redis/go-redis/v9"
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

//Define a read-only structure to hold the synchronized configuration.
type SessionTimeoutConfig struct{
  Timeout time.Duration
  /***
  To avoid overloading your Redis instance and the client's browser with cookie updates on every single click, you should implement
  a "lazy refresh" (or debounced refresh) strategy.

  Instead of updating the session every single time, you check how much time has passed since the session started or when it was
  last updated. You only issue the updates if a specific threshold has passed -- most commonly when the session is halfway to
  expiring.
  ***/
  Threshold time.Duration
}

//Keep the variables unexported (lowercase) so they can't be modified directly.
var (

  sessionTimeout int64 //jct

  Redis_db *redis.Client
  sessionConfig atomic.Value  //Initialize a single atomic.Value.
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
  //Initialize with default values so .Load() never returns nil.
  SetSessionTimeout(10 * time.Minute)
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
  /***
  https://en.wikipedia.org/wiki/HTTP_cookie
  https://httpwg.org/specs/rfc6265.html

  When a browser sends a cookie back to the Go server in a request header, it only sends the name and the value (e.g., Cookie: session_token=abc123xyz). The browser strips out the Expires, Path, and Domain fields before sending it over the network.
  ***/
  cookie = &http.Cookie{
    Name: "session_token",
    Value: sessionToken,
    /***
    Whenever you create or delete a cookie, always explicitly set Path: "/". If you omit the path, the browser defaults to the
    current URL directory, which instantly creates duplicate cookie risks when users navigate your site.
    ***/
    Path: "/",  //Always lock down the Path.
    HttpOnly: true,  //Security: prevent XSS access.
    SameSite: http.SameSiteStrictMode,
    Secure: true,  //Security: HTTPS only.
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
    expiry: time.Now().Add(time.Duration(sessionTimeout)),
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
    expiry: time.Now().Add(time.Duration(sessionTimeout)),
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

//------------------------------


func GetSessionTimeoutString() string {
  tc := GetSessionTimeoutConfig()
  return fmt.Sprintf("%02dh%02dm%02ds%05dms", int(tc.Timeout.Hours()), int(tc.Timeout.Minutes())%60,
    int(tc.Timeout.Seconds())%60, int(tc.Timeout.Milliseconds())%1000)
}

func SetSessionTimeout(timeout time.Duration) {
  //Create a brand-new, isolated struct instance.
  newCfg := &SessionTimeoutConfig{
    Timeout: timeout,
    Threshold: timeout >> 1,  //Shift right by 1 to divide by 2.
  }
  //Atomically swap the pointer. This happens in a single CPU instruction.
  sessionConfig.Store(newCfg)
}

//Read safely from anywhere without locks.
func GetSessionTimeoutConfig() *SessionTimeoutConfig {
  return sessionConfig.Load().(*SessionTimeoutConfig)
}

//make sure the client is started once; use singlenton????????????????????????????
func StartRedisServer(ctx context.Context, options *redis.Options) error {
  //Connect to the Redis.
  Redis_db = redis.NewClient(options)
  //Ping the Redis Server to check connection.
  _, err := Redis_db.Ping(ctx).Result()
  return err
}

func SaveRedis(ctx context.Context, userData string) (*http.Cookie, error) {
  timeCfg := GetSessionTimeoutConfig()
  sessionId := GetNewUuid()
  err := Redis_db.Set(ctx, sessionId, userData, timeCfg.Timeout).Err()
  if err != nil {
    return nil, err
  }
  sessionExpiresAt := time.Now().Add(timeCfg.Timeout)
  cookieValue := fmt.Sprintf("%s|%d", sessionId, sessionExpiresAt.Unix())
  cookie := CreateCookie(cookieValue)
  cookie.MaxAge = int(timeCfg.Timeout.Seconds())
  cookie.Expires = sessionExpiresAt
  return cookie, nil
}

/***
func ValidateSessionRedis(req *http.Request) (string, *http.Cookie, error) {
  //Extract cookie (assuming it contains: "uuid|timestamp").
  cookie, err := req.Cookie("session_token")
  if err != nil {
    return "", nil, err
  }
  parts := strings.Split(cookie.Value, "|")
  if len(parts) != 2 {
    return "", nil, errors.New("Invalid cookie format.")
  }
  sessionId := parts[0]
  expiryUnix, err := strconv.ParseInt(parts[1], 10 *base*, 64 *int64*)
  if err != nil {
    return "", nil, errors.New("Invalid expiry format.")
  }
  //Convert timestamp and compute how much time is left.
  expiresAt := time.Unix(expiryUnix, 0)
  //Calculate remaining lifetime left on this cookie.
  timeLeft := time.Until(expiresAt)
  timeCfg := GetSessionConfig()
  // ***
  // To implement an "automatic rolling session refresh," you check how much time has passed since the session started. If the time
  // remaining falls below your Threshold, you generate a new cookie and reset the TTL in Redis.

  // Because your cookie value contains the absolute expiration time (sessionId|expiresAtUnix), you can easily figure out exactly
  // how much time is left without hitting Redis first.
  // ***
  if timeLeft < timeCfg.Threshold {
    //Update backend countdown clock.
    //If the user is active, reset the countdown clock back to sessionTimeout.
    err := Redis_db.Expire(req.Context(), sessionId, timeCfg.Timeout)
    if err != nil {
      // If session missing or Redis down, fail safely
     // http.Error(w, "Internal Server Error", http.StatusInternalServerError)
      return "", nil, err.Err()
    }
    //Update browser cookie with a fresh future timestamp.
    sessionExpiresAt := time.Now().Add(timeCfg.Timeout)
    cookieValue := fmt.Sprintf("%s|%d", sessionId, sessionExpiresAt.Unix())
    cookie = CreateCookie(cookieValue)
    //Update the Cookie expiration to match Redis.
    cookie.MaxAge = int(timeCfg.Timeout.Seconds())
    cookie.Expires = sessionExpiresAt
    return "", cookie, nil
  }
  return "", cookie, nil
}
***/

func DelRedis(ctx context.Context, sessionId string) (int64, error) {
  return Redis_db.Del(ctx, sessionId).Result()
}

func GetRedis(ctx context.Context, sessionId string) (string, error) {
  userData, err := Redis_db.Get(ctx, sessionId).Result()
  if err != nil {
    //Check if the key simply doesn't exist in Redis.
    if errors.Is(err, redis.Nil) {
      logger.LogInfo("GetRedis: The key is not present in the database.", falseCorrelationId)
      return "", err  //User not found.
    } else {
      logger.LogInfo("GetRedis: Underlying network error.", falseCorrelationId)
      return "", err  //Redis Error.
    }
  }
  return userData, nil  //Key exists in Redis and the session is active.
}

func DbSizeRedis(ctx context.Context) (int64, error) {
  return Redis_db.DBSize(ctx).Result()
}


//make sure the client is started once; use singlenton
