package sessions

import (
  "context"
  "crypto/rand"
  "crypto/hmac"
  "crypto/sha256"
  "encoding/base64"
  "encoding/json"
  "errors"
  "fmt"
  //Run this command in your terminal to install the standard JWT library for Go:
  // $ go get -u github.com/golang-jwt/jwt/v5
  "github.com/golang-jwt/jwt/v5"
  //The option -u instructs 'get' to update the module with dependencies.
  //go get -u github.com/redis/go-redis/v9
  //go get github.com/redis/go-redis/v9
  "github.com/redis/go-redis/v9"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "sync/atomic"
  "time"
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
  //Choose a long, secure secret key (keep this safe on the server environment).
  secretHMACKey = []byte("your-super-secure-32-byte-secret-key")//jct
  jwtAdminKey = []byte("your_ultra_secure_secret_key_here")  //jct
  redis_db *redis.Client
  oneRedisClientOnly sync.Once
  sessionConfig atomic.Value  //Initialize a single atomic.Value.
  //Define package-level errors.
  ErrKeyNotFound = errors.New("session not found or expired")
  ErrMalformedCookie = errors.New("malformed session cookie layout")
  ErrTamperedCookie = errors.New("session cookie signature is invalid or tampered")
  ErrInvalidTimestamp = errors.New("session cookie contains an invalid expiration timestamp")
)

func init() {
  //Initialize with default values so .Load() never returns nil.
  SetSessionTimeout(10 * time.Minute)
  /*** jct
  //Attempt to load the secret key from the environment variables.
  envKey := (os.Getenv("SESSION_SECRET_KEY"))
  if envKey != nil {
    secretHMACKey = []byte(envKey)
  }
  **/
  //Enforce strict security verification: key must be present and strong 32 bytes (256 bits) is the standard required
  //minimum length for HMAC-SHA256.
  if len(secretHMACKey) < 32 {
    panic("CRITICAL CONFIGURATION ERROR: " +
          "The 'SESSION_SECRET_KEY' environment variable must be set and be at least 32 bytes long to secure cookies!")
  }
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

/***
Calling this function to construct a fresh cookie is much cleaner and safer than trying to modify and reuse the incoming,
stripped-down reference variable pulled from the req.Cookie() slice.
***/
func CreateCookie(name, value string) (cookie *http.Cookie) {
  /***
  https://en.wikipedia.org/wiki/HTTP_cookie
  https://httpwg.org/specs/rfc6265.html

  When a browser sends a cookie back to the Go server in a request header, it only sends the name and the value (e.g., Cookie: session_token=abc123xyz). The browser strips out the Expires, Path, and Domain fields before sending it over the network.
  ***/
  cookie = &http.Cookie{
    Name: name,
    Value: value,
    /***
    Whenever you create or delete a cookie, always explicitly set Path: "/". If you omit the path, the browser defaults to the
    current URL directory, which instantly creates duplicate cookie risks when users navigate your site.
    ***/
    Path: "/",  //Always lock down the Path; accessible across the entire domain scope.
    MaxAge: -1,  //Instruct the browser to delete immediately.
    Expires: time.Unix(1, 0),
    /***
    Since we are running over HTTPS, we should strictly enforce these three security flags on the http.Cookie struct when
    creating or updating it.
    ***/
    HttpOnly: true,  //Prevent JavaScript (XSS attacks) from reading the cookie.
    SameSite: http.SameSiteStrictMode,  //Block cross-site request context leaks (add CSRF layer defense).
    Secure: true,  //Force the browser to ONLY send the cookie over HTTPS.
  }
  return
}

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
func VerifyHealthRedis() error {
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

func SetRedis(ctx context.Context, userName string) (*http.Cookie, error) {
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
  //Convert "uuid|timestamp" into "uuid|timestamp|signature".
  signedCookieValue := SignCookieValue(sessionId, sessionExpiresAt.Unix())
  cookie := CreateCookie("session_token", signedCookieValue)
  cookie.MaxAge = int(timeCfg.Timeout.Seconds())
  cookie.Expires = sessionExpiresAt
  return cookie, nil
}

func HSetRedis(ctx context.Context, userName, partitionName string, jsonBytes []byte) error {
  timeCfg := GetSessionTimeoutConfig()
  key := "user:data:" + userName
  //Write the fields into the Redis Hash.
  err := redis_db.HSet(ctx, key, partitionName, string(jsonBytes)).Err()
  if err != nil {
    return err
  }
  /***
  HSet itself does not have a built-in parameter to accept a TTL duration like Set does. To apply an expiration to a Redis Hash,
  you must perform a two-step operation: first, write or update the fields using HSet, and then immediately apply the timeout
  to the entire key using the Expire method.

  Because we are using the root key "user:data: + userName", the entire Redis Hash shares a single TTL (Time-To-Live).
  * Logout/Timeout: When the root key expires or is deleted via sess.DelRedis(ctx, "user:data:" + userName), all partitions inside
  it are wiped out instantly.
  * Rolling Timeout: When we execute sess.ExpireRedis(ctx, "user:data:" + userName, timeCfg.Timeout) inside the rolling refresh,
  the lifecycle of all partitions is bumped simultaneously.
  ***/
  err = redis_db.Expire(ctx, key, timeCfg.Timeout).Err()
  if err != nil {
    return err
  }
  return nil
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

func HGetRedis(ctx context.Context, key, partitionName string) (string, error) {
  //Fetch only the JSON string for the specific partition.
  jsonStr, err := redis_db.HGet(ctx, key, partitionName).Result()
  if err != nil {
    if errors.Is(err, redis.Nil) {
      return "", ErrKeyNotFound  //Partition or user data does not exist.
    }
    return "", err  //Redis error.
  }
  return jsonStr, nil
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

/***
Call when creating or refreshing a cookie.
Because everything happens strictly in-memory using infallible standard library operations, SignCookieValue is a deterministic,
pure function. It will either succeed 100% of the time, or the entire Go runtime itself has crashed (e.g., out of physical RAM).

The only way this function could cause an application issue is if the secretHMACKey is completely empty or hasn't been loaded
from the environment variable yet. If the key is empty, the function still won't crash; it will just generate a weak,
unsecure signature.
***/
func SignCookieValue(sessionId string, expiryUnix int64) string {
  //Recreate your original plain-text layout
  payload := fmt.Sprintf("%s|%d", sessionId, expiryUnix)
  //Create a cryptographic hash of that payload using the secret key.
  mac := hmac.New(sha256.New, secretHMACKey)
  //Because underlying cryptographic hash state updates are memory-only operations, they cannot fail. It always returns nil for errors.
  mac.Write([]byte(payload))
  signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
  //Return the final 3-part cookie string.
  return payload + "|" + signature
}

/***
It parses and validates the client-side session cookie string.
VerifyAndSplitCookie is completely safe against malicious input. Unlike the signing function, the verification function parses
raw strings sent directly from the client's browser, which means it must be designed to catch malformed data, unexpected
characters, and empty tokens without panicking.
***/
func VerifyAndSplitCookie(cookieValue string) (string, int64, error) {
  /***
  Checking len(cookieValue) < 40 at the very top cuts off heavy execution immediately if an attacker tries to flood the middleware
  route headers with thousands of small, broken string fragments.
  ***/
  if len(cookieValue) < 40 {  //A minimal layout "uuid|timestamp|signature" will always exceed 40 chars.
    return "", 0, ErrMalformedCookie
  }
  //Split the token on the pipe separator.
  parts := strings.Split(cookieValue, "|")
  if len(parts) != 3 {
    return "", 0, ErrMalformedCookie
  }
  sessionId := parts[0]
  expiryTime := parts[1]
  incomingSignature := parts[2]
  //Guard against blank structural sub-strings (e.g., "||signature" or "uuid||signature").
  if sessionId == "" || expiryTime == "" || incomingSignature == "" {
    return "", 0, ErrMalformedCookie
  }
  //Recreate the payload to verify against the signature.
  payload := sessionId + "|" + expiryTime
  mac := hmac.New(sha256.New, secretHMACKey)
  mac.Write([]byte(payload))
  expectedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
  //Use ConstantTimeCompare to prevent timing attacks.
  //If the user modified the timestamp or ID, the signature check fails immediately here.
  if !hmac.Equal([]byte(incomingSignature), []byte(expectedSignature)) {
    return "", 0, ErrTamperedCookie
  }
  /***
  Parse the Unix timestamp.
  We check the cryptographic HMAC signature before calling strconv.ParseInt. String conversion routines can occasionally consume
  noticeable processing time if malicious input contains millions of arbitrary text numbers. By validating the signature first,
  the hot path middleware guarantees it will only run numerical conversions on packets originally sealed by your server.
  ***/
  expiryUnix, err := strconv.ParseInt(expiryTime, 10 /*base*/, 64 /*int64*/)
  if err != nil {
    return "", 0, err
  }
  return sessionId, expiryUnix, nil
}

/***
Verification of JWT.
1. Extraction: The server retrieves the token from the incoming HTTP request (usually from the Authorization: Bearer <token> header
   or a secure cookie).
2. Signature Re-calculation: The server extracts the Header and Payload from the token, joins them, and hashes them using the algorithm
   specified in the header and the server's private/secret key. If this newly generated signature matches the signature attached to the
   token, it proves the payload has not been tampered with.
3. Claims Validation: Once the signature is proven authentic, the server evaluates standard time-based fields embedded in the payload,
   ensuring the current time is before the expiration time and after the "not before" time.
***/
func ValidateJwtToken(tokenString string) (jwt.MapClaims, error) {
  token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
    /***
    Verify signature algorithm is HMAC. Without this check, an attacker could change the token header to {"alg": "none"} or exploit a
    "Symmetric-Asymmetric Key Confusion" vulnerability to bypass security entirely.
    ***/
    if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
      return nil, jwt.ErrSignatureInvalid
    }
    //Return the secret key used to verify the signature.
    return jwtAdminKey, nil
  })
  if err != nil {
    return nil, err
  }
  //Extract and validate claim.
  if claim, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
    return claim, nil
  }
  return nil, jwt.ErrTokenUnverifiable
}

/***
By default, standard JWTs are signed, not encrypted. This means anyone who intercepts the token can decode and read its payload contents.
A common point of confusion is thinking that JWT data are hidden.
Data is public: Standard JWTs are base64-encoded, not encrypted. Anyone who intercepts the token can read the content inside the payload.
Tamper-proof, not secret: The security of a JWT relies entirely on its signature. The server uses a secret key to sign the token. If a
malicious actor modifies the payload, the signature becomes invalid, and it will be rejected.
***/
func GenerateJwtToken(jwtToken bool) (string, error) {
  //JSON Web Tokens require times -- such as expiration (exp) or issued-at (iat) -- to be encoded as a Unix epoch timestamp in seconds.
  claim := jwt.MapClaims{
    "is_admin": jwtToken,
    // "iss": "self",  //Issuer - Who issued the token.
    // "aud": []string{"self"},  //Audience - Who the token is intended for.
    "exp": jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),  //Expires at - When the token expires.  jct
    "iat": jwt.NewNumericDate(time.Now()),  //Issued at - When the token was issued.
    "nbf": jwt.NewNumericDate(time.Now()),  //Not before - When the token becomes valid.
  }
  //Declare signing method HS256.
  token := jwt.NewWithClaims(jwt.SigningMethodHS256, claim)
  //Cryptographically hashes the combined header and payload using the secret key via the standard HMAC-SHA256 (HS256) protocol.
  tokenString, err := token.SignedString(jwtAdminKey)
  if err != nil {
    return "", err
  }
  return tokenString, nil
}
