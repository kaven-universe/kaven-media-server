/********************************************************************
 * @author:      Kaven
 * @email:       kaven@wuwenkai.com
 * @website:     http://blog.kaven.xyz
 * @file:        [kaven-image] /src/common/function.ts
 * @create:      2022-09-01 18:18:42.540
 * @modify:      2026-06-23 16:19:28.516
 * @version:     0.0.2
 * @times:       11
 * @lines:       93
 * @copyright:   Copyright © 2022-2026 Kaven. All Rights Reserved.
 * @description: [description]
 * @license:     [license]
 ********************************************************************/

import { ConsoleLogger, GetFileExtension, LoggingAgent } from "kaven-basic";
import { copyToClipboard } from "quasar";
import type { IHfsFileInfo } from "src/share";
import { RouteName, UrlType } from "./enum";
import type { IFile } from "./interface";

export const Logger = new LoggingAgent(new ConsoleLogger(true));

export function GetRouteByName(name: string) {
    const keys = Object.keys(RouteName);
    const values = Object.values(RouteName);

    return values[keys.indexOf(name)];
}

export function GetFileIcon(info: IHfsFileInfo) {
    if (info.isDirectory) {
        return "folder";
    }

    /* cSpell:disable */
    const icons = ["3g2", "3ga", "3gp", "7z", "aa", "aac", "ac", "accdb", "accdt", "adn", "ai", "aif", "aifc", "aiff", "ait", "amr", "ani", "apk", "app", "applescript", "asax", "asc", "ascx", "asf", "ash", "ashx", "asmx", "asp", "aspx", "asx", "au", "aup", "avi", "axd", "aze", "bak", "bash", "bat", "bin", "blank", "bmp", "bowerrc", "bpg", "browser", "bz2", "c", "cab", "cad", "caf", "cal", "cd", "cer", "cfg", "cfm", "cfml", "cgi", "class", "cmd", "codekit", "coffee", "coffeelintignore", "com", "compile", "conf", "config", "cpp", "cptx", "cr2", "crdownload", "crt", "crypt", "cs", "csh", "cson", "csproj", "css", "csv", "cue", "dat", "db", "dbf", "deb", "dgn", "dist", "diz", "dll", "dmg", "dng", "doc", "docb", "docm", "docx", "dot", "dotm", "dotx", "download", "dpj", "ds_store", "dtd", "dwg", "dxf", "editorconfig", "el", "enc", "eot", "eps", "epub", "eslintignore", "exe", "f4v", "fax", "fb2", "fla", "flac", "flv", "folder", "gadget", "gdp", "gem", "gif", "gitattributes", "gitignore", "go", "gpg", "gz", "h", "handlebars", "hbs", "heic", "hs", "hsl", "htm", "html", "ibooks", "icns", "ico", "ics", "idx", "iff", "ifo", "image", "img", "in", "indd", "inf", "ini", "iso", "j2", "jar", "java", "jpe", "jpeg", "jpg", "js", "json", "jsp", "jsx", "key", "kf8", "kmk", "ksh", "kup", "less", "lex", "licx", "lisp", "lit", "lnk", "lock", "log", "lua", "m", "m2v", "m3u", "m3u8", "m4", "m4a", "m4r", "m4v", "map", "master", "mc", "md", "mdb", "mdf", "me", "mi", "mid", "midi", "mk", "mkv", "mm", "mo", "mobi", "mod", "mov", "mp2", "mp3", "mp4", "mpa", "mpd", "mpe", "mpeg", "mpg", "mpga", "mpp", "mpt", "msi", "msu", "nef", "nes", "nfo", "nix", "npmignore", "odb", "ods", "odt", "ogg", "ogv", "ost", "otf", "ott", "ova", "ovf", "p12", "p7b", "pages", "part", "pcd", "pdb", "pdf", "pem", "pfx", "pgp", "ph", "phar", "php", "pkg", "pl", "plist", "pm", "png", "po", "pom", "pot", "potx", "pps", "ppsx", "ppt", "pptm", "pptx", "prop", "ps", "ps1", "psd", "psp", "pst", "pub", "py", "pyc", "qt", "ra", "ram", "rar", "raw", "rb", "rdf", "resx", "retry", "rm", "rom", "rpm", "rsa", "rss", "rtf", "ru", "rub", "sass", "scss", "sdf", "sed", "sh", "sitemap", "skin", "sldm", "sldx", "sln", "sol", "sql", "sqlite", "step", "stl", "svg", "swd", "swf", "swift", "sys", "tar", "tcsh", "tex", "tfignore", "tga", "tgz", "tif", "tiff", "tmp", "torrent", "ts", "tsv", "ttf", "twig", "txt", "udf", "vb", "vbproj", "vbs", "vcd", "vcs", "vdi", "vdx", "vmdk", "vob", "vscodeignore", "vsd", "vss", "vst", "vsx", "vtx", "war", "wav", "wbk", "webinfo", "webm", "webp", "wma", "wmf", "wmv", "woff", "woff2", "wps", "wsf", "xaml", "xcf", "xlm", "xls", "xlsm", "xlsx", "xlt", "xltm", "xltx", "xml", "xpi", "xps", "xrb", "xsd", "xsl", "xspf", "xz", "yaml", "yml", "z", "zip", "zsh"];
    /* cSpell:enable */

    const ext = GetFileExtension(info.name);

    if (icons.includes(ext)) {
        return ext;
    }

    return "blank";
}

export function OpenInNewTab(url: string) {
    if (url) {
        window.open(url, "_blank")?.focus();
    }
}

export function Download(url: string) {
    if (url) {
        window.open(url, "_self");
    }
}

export function CopyFile(file: IFile, type?: UrlType) {
    let text = file.link;
    const name = file.name;

    if (!text.includes("://")) {
        text = `${window.location.origin}${text}`;
    }

    if (type !== undefined) {
        const url = text;

        switch (type) {
            case UrlType.URL:
                text = url;
                break;

            case UrlType.HTML:
                text = `<img src="${url}" alt="${name}" title="${name}" />`;
                break;

            case UrlType.Markdown:
                text = `![${name}](${url})`;
                break;

            case UrlType.MarkdownWithLink:
                text = `[![${name}](${url})](${url})`;
                break;
        }
    }

    copyToClipboard(text).catch(ex => Logger.Error(ex));
}
