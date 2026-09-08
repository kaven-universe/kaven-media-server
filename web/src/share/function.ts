/********************************************************************
 * @author:      Kaven
 * @email:       kaven@wuwenkai.com
 * @website:     http://blog.kaven.xyz
 * @file:        [kaven-image-server] /src/share/function.ts
 * @create:      2022-09-01 16:07:02.627
 * @modify:      2026-06-26 10:30:39.532
 * @version:     1.0.3
 * @times:       8
 * @lines:       59
 * @copyright:   Copyright © 2022-2026 Kaven. All Rights Reserved.
 * @description: [description]
 * @license:     [license]
 ********************************************************************/

import { GetFileExtension } from "kaven-basic";
import { IHfsFileInfo } from "./interface.js";
import { ErrorCode } from "./enum.js";

export function HfsFileInfoComparer(a: IHfsFileInfo, b: IHfsFileInfo) {
    const aIsDir = a.isDirectory;
    const bIsDir = b.isDirectory;

    if (aIsDir !== bIsDir) {
        if (aIsDir) {
            return -1;
        }

        return 1;
    }

    if (a.name > b.name) {
        return 1;
    } else if (a.name < b.name) {
        return -1;
    }

    return 0;
}

export function IsImage(name?: string) {
    if (!name) {
        return false;
    }

    const images = ["png", "jpg", "gif", "bmp", "pdf", "xml", "jp2", "jpm", "jpx", "mj2"];
    const ext = GetFileExtension(name);

    if (images.includes(ext.toLowerCase())) {
        return true;
    }

    return false;
}

export function ErrorCodeName(code: ErrorCode): string {
    return ErrorCode[code] ?? `Unknown(${code})`;
}
