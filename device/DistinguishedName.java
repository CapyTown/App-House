package com.androidfun;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;

/** Minimal RFC2253 reader; Android does not provide javax.naming. */
final class DistinguishedName {
    private DistinguishedName() {}

    static String organization(String subject) {
        int start = 0;
        boolean escaped = false;
        for (int i = 0; i <= subject.length(); i++) {
            char current = i == subject.length() ? ',' : subject.charAt(i);
            if (!escaped && (current == ',' || current == '+')) {
                String attribute = subject.substring(start, i).trim();
                if (attribute.regionMatches(true, 0, "O=", 0, 2)) {
                    return unescape(attribute.substring(2));
                }
                start = i + 1;
            }
            if (escaped) escaped = false;
            else if (current == '\\') escaped = true;
        }
        return "";
    }

    private static String unescape(String value) {
        StringBuilder result = new StringBuilder();
        for (int i = 0; i < value.length(); i++) {
            char current = value.charAt(i);
            if (current != '\\' || i + 1 == value.length()) {
                result.append(current);
                continue;
            }
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            while (i + 2 < value.length() && value.charAt(i) == '\\'
                    && Character.digit(value.charAt(i + 1), 16) >= 0
                    && Character.digit(value.charAt(i + 2), 16) >= 0) {
                bytes.write(Character.digit(value.charAt(i + 1), 16) * 16
                        + Character.digit(value.charAt(i + 2), 16));
                i += 3;
            }
            if (bytes.size() > 0) {
                result.append(new String(bytes.toByteArray(), StandardCharsets.UTF_8));
                i--;
            } else {
                result.append(value.charAt(++i));
            }
        }
        return result.toString();
    }
}
