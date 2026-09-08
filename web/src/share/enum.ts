/********************************************************************
 * @author:      Kaven
 * @email:       kaven@wuwenkai.com
 * @website:     http://blog.kaven.xyz
 * @file:        [kaven-image-server] /src/share/enum.ts
 * @create:      2026-06-26 09:29:51.228
 * @modify:      2026-06-26 10:30:39.535
 * @version:     1.0.3
 * @times:       3
 * @lines:       23
 * @copyright:   Copyright © 2026 Kaven. All Rights Reserved.
 * @description: [description]
 * @license:     [license]
 ********************************************************************/

export enum ErrorCode {
    None = 0,
    UnexpectedError = 1,

    InvalidFileType = 10,
    FileAlreadyExist = 11,
    FileTooLarge = 12,
    FolderNotFound = 13,
}
