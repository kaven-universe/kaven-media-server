/********************************************************************
 * @author:      Kaven
 * @email:       kaven@wuwenkai.com
 * @website:     http://blog.kaven.xyz
 * @file:        [kaven-image] /src/common/HfsFile.ts
 * @create:      2022-09-01 14:31:25.446
 * @modify:      2026-06-23 16:19:28.514
 * @version:     0.0.2
 * @times:       12
 * @lines:       50
 * @copyright:   Copyright © 2022-2026 Kaven. All Rights Reserved.
 * @description: [description]
 * @license:     [license]
 ********************************************************************/

import type { IHfsFileInfo } from "src/share";
import { GetFileIcon } from "./function";

export class HfsFile implements IHfsFileInfo {
    public constructor(info: IHfsFileInfo) {
        this.name = info.name;
        this.link = info.link;
        this.isDirectory = info.isDirectory ?? false;

        this.size = info.size;
        this.lastModified = info.lastModified ?? "";

        if (this.lastModified) {
            this.LastModifiedDate = new Date(this.lastModified);
        }
    }

    public name: string;
    public link: string;
    public isDirectory: boolean;

    public size?: number | undefined;
    public lastModified: string;

    public LastModifiedDate?: Date;

    public get Icon() {
        return GetFileIcon(this);
    }

    public toString() {
        return this.name;
    }
}
