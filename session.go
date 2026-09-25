package sessions

import (
  "context"
  "crypto/rand"
  "encoding/base64"
  "encoding/json"
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

//Define a read-only structure to hold the information for a session.
type SessionInfo struct{
  UserName string `json:"username"`
  CSRFToken string `json:"csrf_token"`
}

//Keep the variables unexported (lowercase) so they can't be modified directly.
var (

  sessionTimeout int64 //jct

  redis_db *redis.Client
  oneRedisClientOnly sync.Once
  sessionConfig atomic.Value  //Initialize a single atomic.Value.
  //Define package-level errors.
  ErrKeyNotFound = errors.New("session not found or expired")

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


/***
To ensure a CSRF token cannot be guessed by an attacker, generate it using Go's crypto/rand package (never use math/rand).
Encoding it to base64 makes it perfectly safe to transmit over HTTP headers or HTML forms.
***/
func GenerateRandomToken() (string, error) {
  b := make([]byte, 32)  //32 bytes gives 256 bits of entropy.
  _, err := rand.Read(b)
  if err != nil {
    return "", err
  }
  return base64.StdEncoding.EncodeToString(b), nil
}

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

/***
Every single time you call redis.NewClient, the library initializes and returns a completely new client instance with its own
independent, isolated connection pool.

If multiple goroutines call redis.NewClient simultaneously, they will create multiple separate client instances, resulting
in serious performance issues.

The *redis.Client struct is fully thread-safe and designed to be initialized only once during application startup. All your
goroutines should share this single instance.
***/
func GetRedisClient(options *redis.Options) *redis.Client {
  //sync.Once guarantees only the very first goroutine runs this block (Singleton).
  oneRedisClientOnly.Do(func() {
    //Connect to the Redis.
    redis_db = redis.NewClient(options)
  })
  return redis_db
}

/***
To fully verify that your Redis client is up, running, and ready for production traffic, you can combine redis.Ping() with
redis.DBSize() or a lightweight write/read operation.
The Enhanced Health Check Pattern
***/
func VerifyHealth() error {
  if redis_db == nil {
    return errors.New("redis client is not initialized")
  }
  //Create a strict timeout context specifically for the health check.
  ctx, cancel := context.WithTimeout(context.Background(), 2 * time.Second)
  defer cancel()
  //Step 1: Ping to verify the TCP connection and authentication.
  if err := redis_db.Ping(ctx).Err(); err != nil {
    return fmt.Errorf("network ping failed: %w", err)
  }
  //Step 2: Run a fast O(1) database operation to verify execution readiness.
  //If Redis runs out of memory (OOM), Ping might succeed but DBSize or writes will fail.
  _, err := redis_db.DBSize(ctx).Result()
  if err != nil {
    return fmt.Errorf("database operation failed: %w", err)
  }
  return nil
}

/***
If you want to absolutely guarantee that your client has write permissions (crucial if you are using Redis Master/Replica
architectures where your app might accidentally connect to a read-only replica), you can run a quick write-and-delete test
during startup.
Write/Read "Canary" Test (Deepest Verification)
***/
func VerifyWritePermissions() error {
  ctx, cancel := context.WithTimeout(context.Background(), 2 * time.Second)
  defer cancel()
  key := "healthcheck:canary"
  //1. Test Writing with a tiny TTL so it cleans itself up automatically
  err := redis_db.Set(ctx, key, "1", 10 * time.Second).Err()
  if err != nil {
    return fmt.Errorf("redis write test failed (is it read-only?): %w", err)
  }
  //2. Test Deleting right away
  err = redis_db.Del(ctx, key).Err()
  if err != nil {
    return fmt.Errorf("redis cleanup test failed: %w", err)
  }
  return nil
}

func SaveRedis(ctx context.Context, userName string) (*http.Cookie, error) {
  timeCfg := GetSessionTimeoutConfig()
  //Setting the key to "session:" + sessionID creates a structured namespace in Redis.
  sessionId, _ := GenerateRandomToken()
  /***
  The industry standard is to use a per-session CSRF token -- generating a single token when the session is created (at login)
  and keeping it valid until the session expires or the user logs out.
  ***/
  csrfToken, _ := GenerateRandomToken()
  sessionInfo := SessionInfo{
    UserName: userName,
    CSRFToken: csrfToken,
  }
  jsonBytes, err := json.Marshal(sessionInfo)
  if err != nil {
    return nil, err
  }
  err = redis_db.Set(ctx, "session:" + sessionId, jsonBytes, timeCfg.Timeout).Err()
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
Remove specified keys from the database. If a key exists, its associated data is wiped, and its memory is reclaimed. If a key
does not exist, it is simply ignored.
It returns two types:
1. int64: The count of keys that were successfully deleted. It will return 0 if none of the provided keys existed.
2. error: Any network or Redis connection error that occurred during execution.
***/
func DelRedis(ctx context.Context, key string) (int64, error) {
  return redis_db.Del(ctx, key).Result()
}

/***
Retrieve the string value associated with a specified key.
It returns two types:
Key Exists          ->  The exact string     nil
Key Does not Exists ->  Undefined            redis.Nil
Redis error         ->  Undefined            err (fatal)
***/
func GetRedis(ctx context.Context, key string) ([]byte, error) {
  //Use .Bytes() to extract raw JSON data directly from the network stream.
  dataBytes, err := redis_db.Get(ctx, key).Bytes()
  if err != nil {
    //Check if the key simply doesn't exist in Redis.
    if errors.Is(err, redis.Nil) {
      return nil, ErrKeyNotFound  //User not found.
    }
    return nil, err  //Redis error.
  }
  return dataBytes, nil  //Key exists in Redis and the session is active.
}

/***
Check the keyspace and report back whether a key is present.
When evaluating a single key, the return values are predictable:
Key Exists           ->  1    nil       The key is found in the keyspace.
Key Does Not Exist   ->  0    nil       Crucial: A missing key returns 0 and a nil error (not redis.Nil).
System/Network Error ->  0    error     Treat the count value as undefined (fatal).

Because the command can accept multiple keys at once, it returns an integer (int64) counting exactly how many of those keys exist.
***/
func ExistsRedis(ctx context.Context, key string) (int64, error) {
  return redis_db.Exists(ctx, key).Result()
}

/***
It provides an int64 representing the total number of keys currently stored in the currently selected Redis database.
Successful Call      ->  Count of all keys  nil     Includes all keys, even if they have an expiration (TTL) set.
Empty Database       ->  0                  nil     The database contains exactly zero keys.
System/Network Error ->  0                  error   Treat the totalKeys count as undefined (fatal).
***/
func DbSizeRedis(ctx context.Context) (int64, error) {
  return redis_db.DBSize(ctx).Result()
}

/***
It sets a time-to-live (TTL) timeout on a key. Once the timeout expires, Redis automatically deletes the key.
It returns two types:
Key Exists & TTL Set ->  true    nil      The timeout was successfully established or updated.
Key Does Not Exist   ->  false   nil      Crucial: Missing keys return false, but the error is nil (not redis.Nil).
System/Network Error ->  false   error    Treat the boolean value as undefined (fatal).
***/
func ExpireRedis(ctx context.Context, key string, timeout time.Duration) (bool, error) {
  return redis_db.Expire(ctx, key, timeout).Result()
}
