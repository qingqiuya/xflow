package com.qingqiu.xflow;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.database.Cursor;
import android.net.Uri;
import android.os.ParcelFileDescriptor;

import java.io.File;
import java.io.FileNotFoundException;

/**
 * 极简 ContentProvider：只干一件事 —— 把私有目录里的文件借给别的 app 打开。
 *
 * 为什么不用 androidx 的 FileProvider：这个项目不走 gradle，全靠 javac + android.jar，
 * 拿不到 androidx；自己写一个 30 行就够，还省得引依赖。
 * Uri 形式：content://<包名>.fileprovider/<绝对路径>
 */
public class FileProviderEx extends ContentProvider {

    @Override
    public boolean onCreate() {
        return true;
    }

    @Override
    public ParcelFileDescriptor openFile(Uri uri, String mode) throws FileNotFoundException {
        String path = uri.getPath();
        if (path == null) throw new FileNotFoundException("空路径");
        File f = new File(path);
        if (!f.exists()) throw new FileNotFoundException(path);
        return ParcelFileDescriptor.open(f, ParcelFileDescriptor.MODE_READ_ONLY);
    }

    @Override
    public Cursor query(Uri uri, String[] projection, String selection,
                        String[] selectionArgs, String sortOrder) {
        return null;
    }

    @Override
    public String getType(Uri uri) {
        return null;
    }

    @Override
    public Uri insert(Uri uri, ContentValues values) {
        return null;
    }

    @Override
    public int delete(Uri uri, String selection, String[] selectionArgs) {
        return 0;
    }

    @Override
    public int update(Uri uri, ContentValues values, String selection, String[] selectionArgs) {
        return 0;
    }
}
