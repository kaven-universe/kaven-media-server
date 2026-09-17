/********************************************************************
 * @author:      Kaven
 * @email:       kaven@wuwenkai.com
 * @website:     http://blog.kaven.xyz
 * @file:        [kaven-image-server] /src/share/interface.ts
 * @create:      2022-09-01 16:05:22.183
 * @modify:      2026-06-26 09:29:51.226
 * @version:     1.0.3
 * @times:       13
 * @lines:       43
 * @copyright:   Copyright © 2022-2026 Kaven. All Rights Reserved.
 * @description: [description]
 * @license:     [license]
 ********************************************************************/

import { ErrorCode } from "./enum.js";

export interface IHfsFileInfo {
    name: string;
    link: string;
    isDirectory?: boolean;

    size?: number | undefined;
    lastModified?: string;
}

export interface IFileAdditionalInfo {
    uuid: string;
    name?: string;
}

export interface IImageIdentify {
    id: string;
    uuid: string;
    name: string;
    sha1: string;
}

export interface IUploadResponseData {
    errorCode: ErrorCode;
    image?: IImageIdentify;
}

export interface IServerInfoUploadData {
    maxFileCount: number;
    maxImageFileSize: number;
    maxHfsFileSize: number;
}

export interface IServerInfoResponseData {
    upload: IServerInfoUploadData;
}
