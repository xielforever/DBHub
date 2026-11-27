package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/httpx"
	crypto "github.com/user/dbhub/pkg/crypto"
)

type mfaSecurity struct {
	cipher  *crypto.Cipher
	key     []byte
	limiter *loginLimiter
}

func newMFASecurity(cfg *config.Config, sessions *SessionStore) *mfaSecurity {
	mac := hmac.New(sha256.New, cfg.SecretKey)
	_, _ = mac.Write([]byte("dbhub-identity-mfa-v1"))
	key := mac.Sum(nil)
	cipher, _ := crypto.NewCipher(key)
	limits := *cfg
	limits.SecretKey = key
	limits.LoginMaxAttempts, limits.LoginIPMaxAttempts, limits.LoginWindow = 15, 90, 5*time.Minute
	return &mfaSecurity{cipher: cipher, key: key, limiter: newLoginLimiter(&limits, sessions.client)}
}
func (s *mfaSecurity) digest(kind, value string) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(kind + "\x00" + value))
	return hex.EncodeToString(mac.Sum(nil))
}
func randomIdentityToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
func enrollmentID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6], value[8] = value[6]&15|64, value[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
}
func identityFailure(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "2")
	httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "身份安全服务暂不可用，请稍后重试"})
}
func identityCheckpoint(w http.ResponseWriter, r *http.Request, user *User) bool {
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ActorID = user.ID; d.ActorUsername = user.Username; d.ActorRole = user.Role })
	if err := auditctx.Checkpoint(r.Context()); err != nil {
		identityFailure(w)
		return false
	}
	return true
}
func (h *Handler) factorBudget(w http.ResponseWriter, r *http.Request, subject string) bool {
	wait, err := h.mfa.limiter.allow(r.Context(), subject, r)
	if err != nil {
		identityFailure(w)
		return false
	}
	if wait > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64((wait+time.Second-1)/time.Second), 10))
		httpx.JSON(w, 429, httpx.Envelope{Code: 42900, Message: "安全验证尝试过于频繁，请稍后重试"})
		return false
	}
	return true
}
func (h *Handler) securityUser(w http.ResponseWriter, r *http.Request) *User {
	w.Header().Set("Cache-Control", "no-store")
	if h.security == nil {
		httpx.Fail(w, httpx.BadRequest("当前身份存储不支持 MFA"))
		return nil
	}
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		httpx.Fail(w, httpx.Unauthorized("未认证"))
		return nil
	}
	u, err := h.users.FindByUsername(r.Context(), claims.Username)
	if err != nil || u.ID != claims.Subject {
		identityFailure(w)
		return nil
	}
	return u
}
func passwordMatches(u *User, password string) bool {
	if len(password) == 0 || len(password) > 4096 {
		return false
	}
	valid, err := VerifyPassword(u.PasswordHash, password)
	return err == nil && valid
}
func (h *Handler) MFAStatus(w http.ResponseWriter, r *http.Request) {
	u := h.securityUser(w, r)
	if u == nil {
		return
	}
	state, err := h.security.MFAState(r.Context(), u.ID)
	if err != nil {
		identityFailure(w)
		return
	}
	httpx.OK(w, map[string]any{"enabled": state.Enabled, "recovery_codes_remaining": state.RecoveryCount})
}
func (h *Handler) MFASetup(w http.ResponseWriter, r *http.Request) {
	u := h.securityUser(w, r)
	if u == nil {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !h.factorBudget(w, r, u.Username) {
		return
	}
	if !passwordMatches(u, input.Password) {
		httpx.Fail(w, httpx.BadRequest("当前密码验证失败"))
		return
	}
	secret, err := newTOTPSecret()
	if err != nil {
		identityFailure(w)
		return
	}
	image, err := totpEnrollmentPNG(secret, u.Username)
	if err != nil {
		identityFailure(w)
		return
	}
	id, err := enrollmentID()
	if err != nil {
		identityFailure(w)
		return
	}
	encrypted, err := h.mfa.cipher.Encrypt(strconv.FormatInt(u.ID, 10) + ":" + secret)
	if err != nil || !identityCheckpoint(w, r, u) {
		if err != nil {
			identityFailure(w)
		}
		return
	}
	expires := time.Now().Add(5 * time.Minute)
	if err = h.security.StartMFASetup(r.Context(), u, id, encrypted, expires); err != nil {
		if !errors.Is(err, ErrMFAInvalid) {
			identityFailure(w)
			return
		}
		httpx.Fail(w, httpx.BadRequest("绑定已失效或 MFA 已启用，请刷新状态"))
		return
	}
	httpx.OK(w, map[string]any{"setup_id": id, "qr_image": image, "expires_at": expires.UTC()})
}
func (h *Handler) secretFor(uid int64, encrypted string) (string, error) {
	plain, err := h.mfa.cipher.Decrypt(encrypted)
	prefix := strconv.FormatInt(uid, 10) + ":"
	if err != nil || !strings.HasPrefix(plain, prefix) || len(plain) != len(prefix)+32 {
		return "", ErrMFAInvalid
	}
	return strings.TrimPrefix(plain, prefix), nil
}
func (h *Handler) MFAEnable(w http.ResponseWriter, r *http.Request) {
	u := h.securityUser(w, r)
	if u == nil {
		return
	}
	var input struct {
		SetupID string `json:"setup_id"`
		Code    string `json:"code"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !h.factorBudget(w, r, u.Username) {
		return
	}
	state, err := h.security.MFAState(r.Context(), u.ID)
	if err != nil {
		identityFailure(w)
		return
	}
	if state.Enabled || state.PendingID != input.SetupID || state.PendingExpires == nil || !state.PendingExpires.After(time.Now()) {
		httpx.Fail(w, httpx.BadRequest("绑定已失效，请重新开始"))
		return
	}
	secret, err := h.secretFor(u.ID, state.EncryptedPending)
	if err != nil {
		identityFailure(w)
		return
	}
	counter, valid := verifyTOTP(secret, input.Code, -1, time.Now())
	if !valid {
		httpx.Fail(w, httpx.BadRequest("动态验证码无效或已过期"))
		return
	}
	codes, hashes := make([]string, 10), make([]string, 10)
	for i := range codes {
		value := make([]byte, 16)
		if _, err = rand.Read(value); err != nil {
			identityFailure(w)
			return
		}
		codes[i] = hex.EncodeToString(value)
		hashes[i] = h.mfa.digest("recovery", strconv.FormatInt(u.ID, 10)+":"+codes[i])
	}
	if !identityCheckpoint(w, r, u) {
		return
	}
	if err = h.security.EnableMFA(r.Context(), u, input.SetupID, state.Version, counter, hashes); err != nil {
		if !errors.Is(err, ErrMFAInvalid) {
			identityFailure(w)
			return
		}
		httpx.Fail(w, httpx.BadRequest("绑定状态已变化，请刷新后重试"))
		return
	}
	u.AuthVersion++
	h.issueSession(w, r, u, codes)
}
func (h *Handler) factor(u *User, state *MFAState, code, recovery string) (int64, string, bool) {
	if !state.Enabled || (code == "") == (recovery == "") {
		return 0, "", false
	}
	if code != "" {
		secret, err := h.secretFor(u.ID, state.EncryptedSecret)
		if err != nil {
			return 0, "", false
		}
		counter, ok := verifyTOTP(secret, code, state.LastCounter, time.Now())
		return counter, "", ok
	}
	recovery = strings.ToLower(strings.TrimSpace(recovery))
	if len(recovery) != 32 {
		return 0, "", false
	}
	if _, err := hex.DecodeString(recovery); err != nil {
		return 0, "", false
	}
	return -1, h.mfa.digest("recovery", strconv.FormatInt(u.ID, 10)+":"+recovery), true
}
func (h *Handler) MFAVerify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Challenge string `json:"challenge_token"`
		Code      string `json:"code"`
		Recovery  string `json:"recovery_code"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if h.security == nil || len(input.Challenge) != 43 {
		httpx.Fail(w, httpx.Unauthorized("安全验证无效，请重新登录"))
		return
	}
	hash := h.mfa.digest("challenge", input.Challenge)
	if !h.factorBudget(w, r, "challenge:"+hash) {
		return
	}
	challenge, err := h.security.AttemptMFAChallenge(r.Context(), hash)
	if err != nil {
		if !errors.Is(err, ErrMFAInvalid) {
			identityFailure(w)
			return
		}
		httpx.Fail(w, httpx.Unauthorized("安全验证无效或尝试次数已用尽，请重新登录"))
		return
	}
	u, err := h.users.FindByUsername(r.Context(), challenge.Username)
	if err != nil {
		userFailure(w, err, "用户不可用")
		return
	}
	if u.ID != challenge.UserID || PasswordVersion(u) != challenge.PasswordVersion {
		httpx.Fail(w, httpx.Unauthorized("安全验证已失效，请重新登录"))
		return
	}
	state, err := h.security.MFAState(r.Context(), u.ID)
	if err != nil {
		identityFailure(w)
		return
	}
	counter, recovery, ok := h.factor(u, state, input.Code, input.Recovery)
	if !ok {
		httpx.Fail(w, httpx.Unauthorized("验证码无效、已使用或已过期"))
		return
	}
	if !identityCheckpoint(w, r, u) {
		return
	}
	if err = h.security.ConsumeMFAChallenge(r.Context(), u, hash, state.Version, counter, recovery); err != nil {
		if !errors.Is(err, ErrMFAInvalid) {
			identityFailure(w)
			return
		}
		httpx.Fail(w, httpx.Unauthorized("验证码无效、已使用或已过期"))
		return
	}
	h.issueSession(w, r, u, nil)
}
func (h *Handler) MFADisable(w http.ResponseWriter, r *http.Request) {
	u := h.securityUser(w, r)
	if u == nil {
		return
	}
	var input struct {
		Password string `json:"password"`
		Code     string `json:"code"`
		Recovery string `json:"recovery_code"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !h.factorBudget(w, r, u.Username) {
		return
	}
	if !passwordMatches(u, input.Password) {
		httpx.Fail(w, httpx.BadRequest("当前密码验证失败"))
		return
	}
	state, err := h.security.MFAState(r.Context(), u.ID)
	if err != nil {
		identityFailure(w)
		return
	}
	counter, recovery, ok := h.factor(u, state, input.Code, input.Recovery)
	if !ok {
		httpx.Fail(w, httpx.BadRequest("验证码无效、已使用或已过期"))
		return
	}
	if !identityCheckpoint(w, r, u) {
		return
	}
	if err = h.security.DisableMFA(r.Context(), u, state.Version, counter, recovery); err != nil {
		if !errors.Is(err, ErrMFAInvalid) {
			identityFailure(w)
			return
		}
		httpx.Fail(w, httpx.BadRequest("验证码无效、已使用或已过期"))
		return
	}
	u.AuthVersion++
	h.issueSession(w, r, u, nil)
}
