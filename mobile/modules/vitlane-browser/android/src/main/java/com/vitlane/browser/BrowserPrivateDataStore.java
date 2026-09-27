package com.vitlane.browser;

import android.content.ContentValues;
import android.content.Context;
import android.database.Cursor;
import android.database.sqlite.SQLiteDatabase;
import android.database.sqlite.SQLiteOpenHelper;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;

import org.json.JSONArray;
import org.json.JSONObject;

import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import java.util.Locale;

import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.Mac;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

/** Encrypted, device-local personal data, secrets and their encrypted use records. */
public final class BrowserPrivateDataStore extends SQLiteOpenHelper {
    private static final String DB_NAME = "vitlane_private_data.db";
    private static final String KEY_ALIAS = "vitlane.browser.personal.v1";
    private static final String LOOKUP_KEY_ALIAS = "vitlane.browser.secret.lookup.v1";
    private static final int VERSION = 2;
    private static final String KINDS = "name|recipient|address|postcode|phone|email";
    private static final String SECRET_KINDS = "password|otp|card_number|card_expiry|card_exp_month|card_exp_year|card_cvc";
    private static final long OTP_MAX_AGE_MS = 10 * 60 * 1000L;

    public static final class SavedValue {
        public final long id;
        public final String kind;
        public final String value;
        SavedValue(long id, String kind, String value) { this.id = id; this.kind = kind; this.value = value; }
    }

    /** Returned only to native secure input UI. Never put this object in chat, model context or logs. */
    public static final class SavedSecret {
        public final long id;
        public final String kind;
        public final String value;
        public final long updatedAt;
        SavedSecret(long id, String kind, String value, long updatedAt) {
            this.id = id; this.kind = kind; this.value = value; this.updatedAt = updatedAt;
        }
    }

    public BrowserPrivateDataStore(Context context) {
        super(context.getApplicationContext(), DB_NAME, null, VERSION);
    }

    @Override public void onCreate(SQLiteDatabase db) {
        createPersonalTables(db);
        createSecretTables(db);
    }

    private static void createPersonalTables(SQLiteDatabase db) {
        db.execSQL("CREATE TABLE IF NOT EXISTS personal (id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL UNIQUE, value_cipher TEXT NOT NULL, updated_at INTEGER NOT NULL)");
        db.execSQL("CREATE TABLE IF NOT EXISTS usage_log (id INTEGER PRIMARY KEY AUTOINCREMENT, personal_id INTEGER NOT NULL, origin_cipher TEXT NOT NULL, purpose_cipher TEXT NOT NULL, used_at INTEGER NOT NULL, FOREIGN KEY(personal_id) REFERENCES personal(id) ON DELETE CASCADE)");
        db.execSQL("CREATE INDEX IF NOT EXISTS usage_personal_time ON usage_log(personal_id, used_at DESC)");
    }

    private static void createSecretTables(SQLiteDatabase db) {
        // Scope and label are encrypted. scope_hash only supports equality lookup and cannot reveal a secret value.
        db.execSQL("CREATE TABLE IF NOT EXISTS secrets (id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, scope_hash TEXT NOT NULL, scope_cipher TEXT NOT NULL, label_cipher TEXT NOT NULL, value_cipher TEXT NOT NULL, updated_at INTEGER NOT NULL, UNIQUE(kind, scope_hash))");
        db.execSQL("CREATE TABLE IF NOT EXISTS secret_usage (id INTEGER PRIMARY KEY AUTOINCREMENT, secret_id INTEGER NOT NULL, origin_cipher TEXT NOT NULL, purpose_cipher TEXT NOT NULL, used_at INTEGER NOT NULL, FOREIGN KEY(secret_id) REFERENCES secrets(id) ON DELETE CASCADE)");
        db.execSQL("CREATE INDEX IF NOT EXISTS secret_scope ON secrets(kind, scope_hash)");
        db.execSQL("CREATE INDEX IF NOT EXISTS secret_usage_time ON secret_usage(secret_id, used_at DESC)");
    }

    @Override public void onConfigure(SQLiteDatabase db) { db.setForeignKeyConstraintsEnabled(true); }

    @Override public void onUpgrade(SQLiteDatabase db, int oldVersion, int newVersion) {
        if (oldVersion < 2) createSecretTables(db);
        if (newVersion > VERSION) throw new IllegalStateException("지원하지 않는 개인정보 DB 버전입니다.");
    }

    public synchronized SavedValue save(String kind, String value) throws Exception {
        String safeKind = requireKind(kind), safeValue = requireValue(safeKind, value);
        ContentValues row = new ContentValues();
        row.put("value_cipher", encrypt(safeValue));
        row.put("updated_at", System.currentTimeMillis());
        SQLiteDatabase db = getWritableDatabase();
        long id = idForKind(db, safeKind);
        if (id > 0) db.update("personal", row, "id=?", new String[]{String.valueOf(id)});
        else { row.put("kind", safeKind); db.insertOrThrow("personal", null, row); }
        return get(safeKind);
    }

    private static long idForKind(SQLiteDatabase db, String kind) {
        try (Cursor cursor = db.query("personal", new String[]{"id"}, "kind=?",
                new String[]{kind}, null, null, null, "1")) {
            return cursor.moveToFirst() ? cursor.getLong(0) : 0;
        }
    }

    public synchronized SavedValue get(String kind) throws Exception {
        String safeKind = requireKind(kind);
        try (Cursor cursor = getReadableDatabase().query("personal", new String[]{"id", "value_cipher"},
                "kind=?", new String[]{safeKind}, null, null, null, "1")) {
            if (!cursor.moveToFirst()) return null;
            return new SavedValue(cursor.getLong(0), safeKind, decrypt(cursor.getString(1)));
        }
    }

    public synchronized void recordUse(long personalId, String origin, String purpose) throws Exception {
        String safeOrigin = clean(origin, 1000), safePurpose = clean(purpose, 1000);
        if (personalId <= 0 || safeOrigin.isEmpty() || safePurpose.isEmpty()) throw new IllegalArgumentException();
        ContentValues row = new ContentValues();
        row.put("personal_id", personalId);
        row.put("origin_cipher", encrypt(safeOrigin));
        row.put("purpose_cipher", encrypt(safePurpose));
        row.put("used_at", System.currentTimeMillis());
        getWritableDatabase().insertOrThrow("usage_log", null, row);
    }

    public synchronized SavedSecret saveSecret(String kind, String origin, String label, String value) throws Exception {
        String safeKind = requireSecretKind(kind), safeValue = requireSecretValue(safeKind, value);
        String scope = secretScope(safeKind, origin), scopeHash = hash(scope);
        ContentValues row = new ContentValues();
        row.put("scope_cipher", encrypt(scope));
        row.put("label_cipher", encrypt(clean(label, 160)));
        row.put("value_cipher", encrypt(safeValue));
        row.put("updated_at", System.currentTimeMillis());
        SQLiteDatabase db = getWritableDatabase();
        long id = secretId(db, safeKind, scopeHash);
        if (id > 0) db.update("secrets", row, "id=?", new String[]{String.valueOf(id)});
        else { row.put("kind", safeKind); row.put("scope_hash", scopeHash); db.insertOrThrow("secrets", null, row); }
        return getSecret(safeKind, origin);
    }

    private static long secretId(SQLiteDatabase db, String kind, String scopeHash) {
        try (Cursor cursor = db.query("secrets", new String[]{"id"}, "kind=? AND scope_hash=?",
                new String[]{kind, scopeHash}, null, null, null, "1")) {
            return cursor.moveToFirst() ? cursor.getLong(0) : 0;
        }
    }

    /** OTP values remain encrypted but are not offered for autofill once they are likely expired. */
    public synchronized SavedSecret getSecret(String kind, String origin) throws Exception {
        String safeKind = requireSecretKind(kind);
        String scopeHash = hash(secretScope(safeKind, origin));
        try (Cursor cursor = getReadableDatabase().query("secrets",
                new String[]{"id", "value_cipher", "updated_at"}, "kind=? AND scope_hash=?",
                new String[]{safeKind, scopeHash}, null, null, null, "1")) {
            if (!cursor.moveToFirst()) return null;
            long updatedAt = cursor.getLong(2);
            if ("otp".equals(safeKind) && System.currentTimeMillis() - updatedAt > OTP_MAX_AGE_MS) return null;
            return new SavedSecret(cursor.getLong(0), safeKind, decrypt(cursor.getString(1)), updatedAt);
        }
    }

    public synchronized void recordSecretUse(long secretId, String origin, String purpose) throws Exception {
        String safeOrigin = origin(origin), safePurpose = clean(purpose, 1000);
        if (secretId <= 0 || safePurpose.isEmpty()) throw new IllegalArgumentException();
        ContentValues row = new ContentValues();
        row.put("secret_id", secretId);
        row.put("origin_cipher", encrypt(safeOrigin));
        row.put("purpose_cipher", encrypt(safePurpose));
        row.put("used_at", System.currentTimeMillis());
        getWritableDatabase().insertOrThrow("secret_usage", null, row);
    }

    /** Metadata only. Values, sites and purposes never enter model context or logs. */
    public synchronized JSONArray summary() throws Exception {
        JSONArray result = new JSONArray();
        String sql = "SELECT p.kind,p.updated_at,COUNT(u.id),MAX(u.used_at) FROM personal p LEFT JOIN usage_log u ON p.id=u.personal_id GROUP BY p.id ORDER BY p.updated_at DESC";
        try (Cursor cursor = getReadableDatabase().rawQuery(sql, null)) {
            while (cursor.moveToNext()) result.put(new JSONObject()
                    .put("kind", cursor.getString(0)).put("updatedAt", cursor.getLong(1))
                    .put("useCount", cursor.getInt(2)).put("lastUsedAt", cursor.isNull(3) ? 0 : cursor.getLong(3)));
        }
        return result;
    }

    /** Native settings UI only. This contains metadata, never a decrypted secret value. */
    public synchronized JSONArray secretSummary() throws Exception {
        JSONArray result = new JSONArray();
        String sql = "SELECT s.kind,s.updated_at,COUNT(u.id),MAX(u.used_at) FROM secrets s LEFT JOIN secret_usage u ON s.id=u.secret_id GROUP BY s.id ORDER BY s.updated_at DESC";
        try (Cursor cursor = getReadableDatabase().rawQuery(sql, null)) {
            while (cursor.moveToNext()) result.put(new JSONObject()
                    .put("kind", cursor.getString(0)).put("updatedAt", cursor.getLong(1))
                    .put("useCount", cursor.getInt(2)).put("lastUsedAt", cursor.isNull(3) ? 0 : cursor.getLong(3)));
        }
        return result;
    }

    /** Native settings UI only. Decrypted sites and purposes must never enter model context or logs. */
    public synchronized JSONArray recentUsage() throws Exception {
        JSONArray result = new JSONArray();
        String sql = "SELECT p.kind,u.origin_cipher,u.purpose_cipher,u.used_at FROM usage_log u JOIN personal p ON p.id=u.personal_id ORDER BY u.used_at DESC LIMIT 20";
        try (Cursor cursor = getReadableDatabase().rawQuery(sql, null)) {
            while (cursor.moveToNext()) result.put(new JSONObject().put("kind", cursor.getString(0))
                    .put("origin", decrypt(cursor.getString(1))).put("purpose", decrypt(cursor.getString(2)))
                    .put("usedAt", cursor.getLong(3)));
        }
        return result;
    }

    /** Native settings UI only. Decrypted use metadata must never enter model context or logs. */
    public synchronized JSONArray recentSecretUsage() throws Exception {
        JSONArray result = new JSONArray();
        String sql = "SELECT s.kind,u.origin_cipher,u.purpose_cipher,u.used_at FROM secret_usage u JOIN secrets s ON s.id=u.secret_id ORDER BY u.used_at DESC LIMIT 20";
        try (Cursor cursor = getReadableDatabase().rawQuery(sql, null)) {
            while (cursor.moveToNext()) result.put(new JSONObject().put("kind", cursor.getString(0))
                    .put("origin", decrypt(cursor.getString(1))).put("purpose", decrypt(cursor.getString(2)))
                    .put("usedAt", cursor.getLong(3)));
        }
        return result;
    }

    public synchronized void clearAll() {
        SQLiteDatabase db = getWritableDatabase();
        db.beginTransaction();
        try {
            db.delete("secret_usage", null, null);
            db.delete("secrets", null, null);
            db.delete("usage_log", null, null);
            db.delete("personal", null, null);
            db.setTransactionSuccessful();
        } finally { db.endTransaction(); }
    }

    static String requireKind(String kind) {
        String value = kind == null ? "" : kind.trim().toLowerCase(Locale.ROOT);
        if (!value.matches(KINDS)) throw new IllegalArgumentException("지원하지 않는 개인정보 종류입니다.");
        return value;
    }

    static String requireSecretKind(String kind) {
        String value = kind == null ? "" : kind.trim().toLowerCase(Locale.ROOT);
        if (!value.matches(SECRET_KINDS)) throw new IllegalArgumentException("지원하지 않는 보안정보 종류입니다.");
        return value;
    }

    static String requireValue(String kind, String value) {
        int limit = "address".equals(kind) ? 500 : "email".equals(kind) ? 254
                : "postcode".equals(kind) ? 32 : "phone".equals(kind) ? 40 : 160;
        String safe = clean(value, 10000);
        if (safe.length() > limit) throw new IllegalArgumentException("입력값이 너무 깁니다.");
        if (safe.isEmpty()
                || safe.matches("(?is).*(?:password|passwd|passcode|otp|cvv|cvc|카드\\s*번호|비밀번호|인증번호).*")
                || looksLikePaymentCard(safe))
            throw new IllegalArgumentException("비밀번호·인증번호·결제정보는 개인정보 칸에 저장할 수 없습니다.");
        return safe;
    }

    static String requireSecretValue(String kind, String value) {
        String safeKind = requireSecretKind(kind);
        if (value == null || value.isEmpty() || value.length() > 512
                || value.matches("(?s).*[\\p{Cc}\\p{Cf}].*")) throw new IllegalArgumentException("보안정보 형식을 확인해 주세요.");
        if ("password".equals(safeKind)) {
            if (value.length() < 4) throw new IllegalArgumentException("비밀번호 형식을 확인해 주세요.");
            return value;
        }
        if ("otp".equals(safeKind)) {
            String compact = value.replaceAll("[ -]", "");
            if (!compact.matches("[0-9]{4,12}")) throw new IllegalArgumentException("인증번호 형식을 확인해 주세요.");
            return compact;
        }
        if ("card_number".equals(safeKind)) {
            String digits = value.replaceAll("[ -]", "");
            if (!digits.matches("[0-9]{13,19}") || !luhn(digits)) throw new IllegalArgumentException("카드번호 형식을 확인해 주세요.");
            return digits;
        }
        if ("card_expiry".equals(safeKind)) {
            String compact = value.trim();
            if (!compact.matches("(?:0[1-9]|1[0-2])(?:[/ -]?)(?:[0-9]{2}|20[0-9]{2})")) throw new IllegalArgumentException("카드 유효기간 형식을 확인해 주세요.");
            return compact;
        }
        if ("card_exp_month".equals(safeKind)) {
            String compact = value.trim();
            if (!compact.matches("(?:0?[1-9]|1[0-2])")) throw new IllegalArgumentException("카드 유효기간 월을 확인해 주세요.");
            return compact;
        }
        if ("card_exp_year".equals(safeKind)) {
            String compact = value.trim();
            if (!compact.matches("(?:[0-9]{2}|20[0-9]{2})")) throw new IllegalArgumentException("카드 유효기간 연도를 확인해 주세요.");
            return compact;
        }
        String digits = value.trim();
        if (!digits.matches("[0-9]{3,4}")) throw new IllegalArgumentException("카드 보안코드 형식을 확인해 주세요.");
        return digits;
    }

    private static boolean looksLikePaymentCard(String value) {
        java.util.regex.Matcher matcher = java.util.regex.Pattern
                .compile("(?<![0-9])(?:[0-9][ -]?){12,18}[0-9](?![0-9])").matcher(value);
        while (matcher.find()) {
            String digits = matcher.group().replaceAll("[^0-9]", "");
            if (digits.length() <= 19 && luhn(digits)) return true;
        }
        return false;
    }

    private static boolean luhn(String digits) {
        int sum = 0;
        boolean twice = false;
        for (int i = digits.length() - 1; i >= 0; i--) {
            int digit = digits.charAt(i) - '0';
            if (twice && (digit *= 2) > 9) digit -= 9;
            sum += digit;
            twice = !twice;
        }
        return sum % 10 == 0;
    }

    private static String secretScope(String kind, String origin) {
        return kind.startsWith("card_") ? "payment-card" : origin(origin);
    }

    private static String origin(String value) {
        try {
            URI parsed = new URI(value == null ? "" : value.trim());
            String scheme = parsed.getScheme(), host = parsed.getHost();
            if (!"https".equalsIgnoreCase(scheme) || host == null || host.isEmpty()
                    || parsed.getUserInfo() != null) throw new IllegalArgumentException();
            int port = parsed.getPort();
            return "https://" + host.toLowerCase(Locale.ROOT) + (port > 0 && port != 443 ? ":" + port : "");
        } catch (Exception error) { throw new IllegalArgumentException("공개 HTTPS 사이트에서만 보안정보를 사용할 수 있습니다."); }
    }

    private String hash(String value) throws Exception {
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(lookupKey());
        return Base64.encodeToString(mac.doFinal(value.getBytes(StandardCharsets.UTF_8)), Base64.NO_WRAP);
    }

    private String encrypt(String plaintext) throws Exception {
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.ENCRYPT_MODE, key());
        byte[] value = cipher.doFinal(plaintext.getBytes(StandardCharsets.UTF_8));
        byte[] iv = cipher.getIV(), result = new byte[iv.length + value.length];
        System.arraycopy(iv, 0, result, 0, iv.length);
        System.arraycopy(value, 0, result, iv.length, value.length);
        return Base64.encodeToString(result, Base64.NO_WRAP);
    }

    private String decrypt(String encoded) throws Exception {
        byte[] all = Base64.decode(encoded, Base64.NO_WRAP);
        if (all.length < 13) throw new IllegalStateException("손상된 암호문입니다.");
        byte[] iv = new byte[12], value = new byte[all.length - 12];
        System.arraycopy(all, 0, iv, 0, 12);
        System.arraycopy(all, 12, value, 0, value.length);
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.DECRYPT_MODE, key(), new GCMParameterSpec(128, iv));
        return new String(cipher.doFinal(value), StandardCharsets.UTF_8);
    }

    private SecretKey key() throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
        if (store.containsAlias(KEY_ALIAS)) return ((KeyStore.SecretKeyEntry)store.getEntry(KEY_ALIAS, null)).getSecretKey();
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
        generator.init(new KeyGenParameterSpec.Builder(KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true).build());
        return generator.generateKey();
    }

    private SecretKey lookupKey() throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
        if (store.containsAlias(LOOKUP_KEY_ALIAS))
            return ((KeyStore.SecretKeyEntry)store.getEntry(LOOKUP_KEY_ALIAS, null)).getSecretKey();
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_HMAC_SHA256, "AndroidKeyStore");
        generator.init(new KeyGenParameterSpec.Builder(LOOKUP_KEY_ALIAS,
                KeyProperties.PURPOSE_SIGN | KeyProperties.PURPOSE_VERIFY)
                .setDigests(KeyProperties.DIGEST_SHA256).build());
        return generator.generateKey();
    }

    private static String clean(String value, int limit) {
        if (value == null) return "";
        String safe = value.replaceAll("[\\p{Cc}\\p{Cf}]", " ").replaceAll("\\s+", " ").trim();
        return safe.length() > limit ? safe.substring(0, limit) : safe;
    }
}
