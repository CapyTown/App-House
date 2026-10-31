package com.androidfun;

import android.content.Context;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.pm.Signature;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.drawable.Drawable;
import android.os.Build;
import android.os.Looper;
import android.os.UserHandle;
import android.util.Base64;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.lang.reflect.Constructor;
import java.lang.reflect.InvocationTargetException;
import java.security.MessageDigest;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.util.Collections;
import java.util.Comparator;
import java.util.List;

/** Read-only package inspection, executed by app_process as the adb shell user. */
public final class Inspector {
    private Inspector() {}

    public static void main(String[] args) {
        int exitCode = 0;
        try {
            if (args.length != 1) {
                throw new IllegalArgumentException("Expected one Android user ID");
            }
            int userId = Integer.parseInt(args[0]);
            if (userId < 0) {
                throw new IllegalArgumentException("Android user ID must be nonnegative");
            }
            if (Build.VERSION.SDK_INT < 26) {
                throw new UnsupportedOperationException("Android 8.0 or newer is required");
            }
            PackageManager manager = shellContext(userId).getPackageManager();
            int flags = PackageManager.GET_PERMISSIONS | PackageManager.MATCH_DISABLED_COMPONENTS;
            flags |= Build.VERSION.SDK_INT >= 28
                    ? PackageManager.GET_SIGNING_CERTIFICATES : PackageManager.GET_SIGNATURES;
            List<PackageInfo> packages = manager.getInstalledPackages(flags);
            Collections.sort(packages, new Comparator<PackageInfo>() {
                @Override public int compare(PackageInfo a, PackageInfo b) {
                    return a.packageName.compareTo(b.packageName);
                }
            });
            for (PackageInfo info : packages) {
                System.out.println(describe(manager, info));
            }
            System.out.println(new JSONObject().put("type", "done").put("count", packages.size()));
        } catch (Throwable error) {
            exitCode = 1;
            try {
                System.out.println(new JSONObject().put("type", "error").put("message", message(error)));
            } catch (Exception ignored) {
                // Diagnostics must not contaminate the JSON-lines stream.
            }
            error.printStackTrace(System.err);
        }
        System.out.flush();
        // ActivityThread starts Binder threads; this is a one-shot command.
        System.exit(exitCode);
    }

    private static Context shellContext(int userId) throws Exception {
        if (Looper.getMainLooper() == null) {
            Looper.prepareMainLooper();
        }
        Class<?> activityThread = Class.forName("android.app.ActivityThread");
        Object thread = activityThread.getMethod("systemMain").invoke(null);
        Context system = (Context) activityThread.getMethod("getSystemContext").invoke(thread);
        Constructor<UserHandle> constructor = UserHandle.class.getDeclaredConstructor(int.class);
        constructor.setAccessible(true);
        UserHandle user = constructor.newInstance(userId);
        // This API is available on Android 8 vendor builds that lack createContextAsUser.
        // Preserve both shell attribution and the explicitly selected Android user.
        return (Context) Context.class.getMethod("createPackageContextAsUser", String.class, int.class, UserHandle.class)
                .invoke(system, "com.android.shell", 0, user);
    }

    private static JSONObject describe(PackageManager manager, PackageInfo info) throws Exception {
        JSONObject result = new JSONObject();
        JSONArray warnings = new JSONArray();
        ApplicationInfo app = info.applicationInfo;
        result.put("type", "app");
        result.put("packageName", info.packageName);
        result.put("name", info.packageName);
        result.put("version", info.versionName == null ? "" : info.versionName);
        result.put("system", app != null && (app.flags & (ApplicationInfo.FLAG_SYSTEM
                | ApplicationInfo.FLAG_UPDATED_SYSTEM_APP)) != 0);
        result.put("enabled", app != null && app.enabled);
        result.put("suspended", app != null && (app.flags & ApplicationInfo.FLAG_SUSPENDED) != 0);
        result.put("icon", "");
        if (app != null) {
            try {
                CharSequence label = manager.getApplicationLabel(app);
                if (label != null && label.length() > 0) result.put("name", label.toString());
            } catch (Exception error) {
                warnings.put("Label: " + message(error));
            }
            try {
                result.put("icon", icon(manager.getApplicationIcon(app)));
            } catch (Exception error) {
                warnings.put("Icon: " + message(error));
            }
        } else {
            warnings.put("Application metadata is unavailable");
        }
        JSONArray permissions = new JSONArray();
        if (info.requestedPermissions != null) {
            for (int i = 0; i < info.requestedPermissions.length; i++) {
                boolean granted = info.requestedPermissionsFlags != null
                        && i < info.requestedPermissionsFlags.length
                        && (info.requestedPermissionsFlags[i] & PackageInfo.REQUESTED_PERMISSION_GRANTED) != 0;
                permissions.put(new JSONObject().put("name", info.requestedPermissions[i]).put("granted", granted));
            }
        }
        result.put("permissions", permissions);
        JSONArray signers = new JSONArray();
        try {
            Signature[] signatures = Build.VERSION.SDK_INT >= 28 && info.signingInfo != null
                    ? info.signingInfo.getApkContentsSigners() : info.signatures;
            if (signatures != null) {
                for (Signature signature : signatures) {
                    try {
                        byte[] encoded = signature.toByteArray();
                        X509Certificate certificate = (X509Certificate) CertificateFactory.getInstance("X.509")
                                .generateCertificate(new ByteArrayInputStream(encoded));
                        String subject = certificate.getSubjectX500Principal().getName("RFC2253");
                        signers.put(new JSONObject().put("subject", subject)
                                .put("organization", DistinguishedName.organization(subject))
                                .put("sha256", hex(MessageDigest.getInstance("SHA-256").digest(encoded))));
                    } catch (Exception error) {
                        warnings.put("Signer: " + message(error));
                    }
                }
            }
        } catch (Exception error) {
            warnings.put("Signers: " + message(error));
        }
        result.put("signers", signers);
        result.put("warnings", warnings);
        return result;
    }

    private static String icon(Drawable drawable) {
        Bitmap bitmap = Bitmap.createBitmap(64, 64, Bitmap.Config.ARGB_8888);
        try {
            Canvas canvas = new Canvas(bitmap);
            drawable.setBounds(0, 0, 64, 64);
            drawable.draw(canvas);
            ByteArrayOutputStream output = new ByteArrayOutputStream();
            if (!bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)) {
                throw new IllegalStateException("PNG encoding failed");
            }
            return Base64.encodeToString(output.toByteArray(), Base64.NO_WRAP);
        } finally {
            bitmap.recycle();
        }
    }

    private static String hex(byte[] bytes) {
        char[] digits = "0123456789abcdef".toCharArray();
        char[] result = new char[bytes.length * 2];
        for (int i = 0; i < bytes.length; i++) {
            result[i * 2] = digits[(bytes[i] & 0xff) >>> 4];
            result[i * 2 + 1] = digits[bytes[i] & 0xf];
        }
        return new String(result);
    }

    private static String message(Throwable error) {
        while (error instanceof InvocationTargetException && error.getCause() != null) {
            error = error.getCause();
        }
        return error.getClass().getSimpleName() + (error.getMessage() == null ? "" : ": " + error.getMessage());
    }
}
