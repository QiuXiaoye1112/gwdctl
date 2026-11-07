#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Google Drive 终端管理工具
"""

import os
import sys
import json
import time
import hmac
import base64
import struct
import random
import string
import shutil
import hashlib
import sqlite3
import subprocess
import urllib.request
import urllib.parse
import urllib.error

INSTALL_DIR = "/opt/gwd"
DB_PATH = "/opt/gwd/data/data.db"
SERVICE = "gwd"
STATIC_HASH_SALT = "https://github.com/alist-org/alist"

# ANSI 颜色
C_RESET = "\033[0m"
C_BOLD = "\033[1m"
C_GREEN = "\033[1;32m"
C_BLUE = "\033[1;34m"
C_YELLOW = "\033[1;33m"
C_RED = "\033[1;31m"
C_CYAN = "\033[1;36m"


def get_db():
    if not os.path.exists(DB_PATH):
        subprocess.run(["systemctl", "start", SERVICE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(15):
            if os.path.exists(DB_PATH):
                break
            time.sleep(0.5)
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    return conn


def hash_password(password: str, salt: str) -> str:
    static_hash = hashlib.sha256(f"{password}-{STATIC_HASH_SALT}".encode("utf-8")).hexdigest()
    return hashlib.sha256(f"{static_hash}-{salt}".encode("utf-8")).hexdigest()


def generate_totp_code(secret: str):
    """根据 Base32 密钥计算当前 RFC 6238 6位动态验证码与剩余秒数"""
    clean = secret.strip().replace(" ", "").upper()
    padding = (8 - len(clean) % 8) % 8
    key = base64.b32decode(clean + "=" * padding, casefold=True)
    now = int(time.time())
    counter = now // 30
    rem = 30 - (now % 30)
    msg = struct.pack(">Q", counter)
    digest = hmac.new(key, msg, hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    code_int = (struct.unpack(">I", digest[offset:offset + 4])[0] & 0x7FFFFFFF) % 1000000
    return f"{code_int:06d}", rem


def restart_service():
    print(f"\n{C_YELLOW}正在重启 Google Drive 服务...{C_RESET}")
    subprocess.run(["systemctl", "restart", SERVICE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    time.sleep(2)
    try:
        p = subprocess.run(
            ["systemctl", "is-active", SERVICE],
            capture_output=True, text=True
        )
        if p.stdout.strip() == "active":
            print(f"{C_GREEN}✓ 服务重启成功，Google Drive 已上线！{C_RESET}\n")
            return True
        else:
            print(f"{C_RED}服务未正常运行，请执行 journalctl -u gwd -n 30 查看日志。{C_RESET}\n")
            return False
    except Exception as e:
        print(f"{C_RED}检查状态出错: {e}{C_RESET}\n")
        return False


def show_status():
    print(f"\n{C_BOLD}=== 当前系统与云盘连接状态 ==={C_RESET}")
    conn = get_db()
    c = conn.cursor()

    c.execute("SELECT username, disabled, otp_secret FROM x_users WHERE role = 2 LIMIT 1")
    admin_row = c.fetchone()
    if admin_row:
        print(f"网页管理员:    {C_GREEN}{admin_row['username']}{C_RESET}")
        has_2fa = bool(admin_row["otp_secret"])
        if has_2fa:
            print(f"双因素验证:    {C_GREEN}已启用 (2FA){C_RESET}")
        else:
            print(f"双因素验证:    {C_YELLOW}未启用{C_RESET}")
    else:
        print(f"网页管理员:    {C_RED}未设置 (请选 3 设置登录密码){C_RESET}")

    c.execute("SELECT id, mount_path, driver, status, addition FROM x_storages LIMIT 1")
    row = c.fetchone()
    conn.close()

    if not row:
        print(f"云盘存储状态:  {C_RED}尚未配置任何存储 (请选 2 配置谷歌云盘凭据){C_RESET}\n")
        return

    try:
        add = json.loads(row["addition"])
    except Exception:
        add = {}

    print(f"驱动类型:      {C_CYAN}{row['driver']}{C_RESET}")
    print(f"挂载路径:      {C_CYAN}{row['mount_path']}{C_RESET}")
    st_color = C_GREEN if row["status"] == "work" else C_RED
    print(f"当前状态:      {st_color}{row['status']}{C_RESET}")
    print(f"连接模式:      {C_BLUE}Google OAuth 2.0 官方直连{C_RESET}")

    cid = add.get("client_id", "")
    cid_masked = cid[:12] + "..." + cid[-24:] if len(cid) > 36 else cid
    print(f"Client ID:     {cid_masked}")

    sec = add.get("client_secret", "")
    sec_masked = sec[:8] + "..." + sec[-4:] if len(sec) > 12 else sec
    print(f"Client Secret: {sec_masked}")

    rt = add.get("refresh_token", "")
    rt_masked = rt[:10] + "..." + rt[-8:] if len(rt) > 18 else rt
    print(f"Refresh Token: {rt_masked}")
    print(f"根文件夹 ID:   {add.get('root_folder_id', 'root')}\n")


def set_admin_account():
    print(f"\n{C_BOLD}{C_BLUE}=== 设置/修改登录密码 ==={C_RESET}")
    conn = get_db()
    c = conn.cursor()
    c.execute("SELECT id, username FROM x_users WHERE role = 2 LIMIT 1")
    row = c.fetchone()

    cur_user = row["username"] if row else "admin"
    username = input(f"请输入网页登录用户名 [直接回车默认 {cur_user}]: ").strip() or cur_user
    password = input("请输入新的网页登录密码 (至少 4 位): ").strip()

    if len(password) < 4:
        conn.close()
        print(f"{C_RED}错误: 密码长度不能少于 4 位！{C_RESET}\n")
        return

    salt = "".join(random.choices(string.ascii_letters + string.digits, k=16))
    pwd_hash = hash_password(password, salt)
    pwd_ts = int(time.time())

    if row:
        c.execute("""
            UPDATE x_users
            SET username = ?, pwd_hash = ?, pwd_ts = ?, salt = ?, disabled = 0, permission = 65535, base_path = '/'
            WHERE id = ?
        """, (username, pwd_hash, pwd_ts, salt, row["id"]))
    else:
        c.execute("""
            INSERT INTO x_users (username, pwd_hash, pwd_ts, salt, password, base_path, role, disabled, permission, otp_secret, sso_id, authn, allow_ldap)
            VALUES (?, ?, ?, ?, '', '/', 2, 0, 65535, '', '', '[]', 0)
        """, (username, pwd_hash, pwd_ts, salt))

    c.execute("UPDATE x_setting_items SET value = 'Google Drive' WHERE key = 'site_title'")
    c.execute("UPDATE x_setting_items SET value = '' WHERE key IN ('announcement', 'hide_files')")
    c.execute("UPDATE x_setting_items SET value = 'true' WHERE key IN ('ignore_system_files', 'auto_update_index')")
    c.execute("UPDATE x_setting_items SET value = 'false' WHERE key = 'sign_all'")
    conn.commit()
    conn.close()
    print(f"{C_GREEN}✓ 网页管理员账号已更新！用户名: {username}{C_RESET}")
    restart_service()


def manage_2fa():
    while True:
        conn = get_db()
        c = conn.cursor()
        c.execute("SELECT id, username, otp_secret FROM x_users WHERE role = 2 LIMIT 1")
        row = c.fetchone()
        conn.close()

        if not row:
            print(f"\n{C_RED}错误: 尚未创建管理员账号，请先选择主菜单 3 设置登录密码。{C_RESET}\n")
            return

        user_id = row["id"]
        username = row["username"]
        secret = (row["otp_secret"] or "").strip()

        print(f"\n{C_BOLD}{C_BLUE}=== 双因素身份验证 (2FA) 管理与恢复 ==={C_RESET}")
        print(f"管理员账号:    {C_CYAN}{username}{C_RESET}")

        if secret:
            try:
                code, rem = generate_totp_code(secret)
            except Exception as e:
                code, rem = f"计算失败({e})", 0
            print(f"当前状态:      {C_GREEN}已启用{C_RESET}")
            print(f"2FA 密钥:      {C_YELLOW}{secret}{C_RESET}")
            print(f"当前动态验证码:{C_BOLD}{C_GREEN} {code} {C_RESET} (剩余有效时间: {rem} 秒)")
            print(f"{C_CYAN}----------------------------------------------------{C_RESET}")
            print("  1. 刷新查看当前 6 位动态验证码")
            print("  2. 重新生成新的 2FA 密钥")
            print("  3. 关闭/取消双因素身份验证 (2FA)")
            print("  0. 返回主菜单")
            sub = input("请选择操作 [0-3，直接回车默认 0]: ").strip() or "0"

            if sub == "1":
                continue
            elif sub == "2":
                new_secret = base64.b32encode(os.urandom(20)).decode("utf-8").rstrip("=")
                conn = get_db()
                conn.execute("UPDATE x_users SET otp_secret = ? WHERE id = ?", (new_secret, user_id))
                conn.commit()
                conn.close()
                print(f"\n{C_GREEN}✓ 已生成并启用新的 2FA 密钥: {new_secret}{C_RESET}")
                restart_service()
            elif sub == "3":
                conn = get_db()
                conn.execute("UPDATE x_users SET otp_secret = '' WHERE id = ?", (user_id,))
                conn.commit()
                conn.close()
                print(f"\n{C_GREEN}✓ 已成功关闭双因素身份验证 (2FA)！现在仅使用密码即可登录。{C_RESET}")
                restart_service()
                break
            elif sub == "0":
                print()
                break
            else:
                print("无效输入。")
        else:
            print(f"当前状态:      {C_YELLOW}未启用{C_RESET}")
            print(f"{C_CYAN}----------------------------------------------------{C_RESET}")
            print("  1. 立即生成并启用 2FA 双因素身份验证")
            print("  0. 返回主菜单")
            sub = input("请选择操作 [0-1，直接回车默认 0]: ").strip() or "0"

            if sub == "1":
                new_secret = base64.b32encode(os.urandom(20)).decode("utf-8").rstrip("=")
                conn = get_db()
                conn.execute("UPDATE x_users SET otp_secret = ? WHERE id = ?", (new_secret, user_id))
                conn.commit()
                conn.close()
                print(f"\n{C_GREEN}✓ 已成功启用 2FA！密钥: {new_secret}{C_RESET}")
                restart_service()
            elif sub == "0":
                print()
                break
            else:
                print("无效输入。")


def exchange_code_for_refresh_token(client_id, client_secret):
    redirect_uri = "http://localhost"
    params = {
        "client_id": client_id,
        "redirect_uri": redirect_uri,
        "response_type": "code",
        "scope": "https://www.googleapis.com/auth/drive",
        "access_type": "offline",
        "prompt": "consent",
    }
    auth_url = "https://accounts.google.com/o/oauth2/v2/auth?" + urllib.parse.urlencode(params)

    print(f"\n{C_YELLOW}步骤 1：请复制下方 Google 官方授权链接，在您的浏览器中打开并登录授权：{C_RESET}")
    print(f"\n{C_CYAN}{auth_url}{C_RESET}\n")
    print(f"{C_YELLOW}步骤 2：授权完成后，浏览器会跳转到 http://localhost/?code=... （页面显示无法访问是正常的）{C_RESET}")
    raw = input("请复制浏览器地址栏的【完整网址】或【code参数值】粘贴到这里: ").strip()
    if not raw:
        return ""

    code = raw
    if "code=" in raw:
        parsed = urllib.parse.urlparse(raw)
        qs = urllib.parse.parse_qs(parsed.query)
        if "code" in qs and qs["code"]:
            code = qs["code"][0]

    token_url = "https://oauth2.googleapis.com/token"
    post_data = urllib.parse.urlencode({
        "code": code,
        "client_id": client_id,
        "client_secret": client_secret,
        "redirect_uri": redirect_uri,
        "grant_type": "authorization_code",
    }).encode("utf-8")

    req = urllib.request.Request(
        token_url,
        data=post_data,
        headers={"Content-Type": "application/x-www-form-urlencoded"}
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            resp_data = json.loads(resp.read().decode("utf-8"))
            rt = resp_data.get("refresh_token", "")
            if rt:
                print(f"{C_GREEN}✓ 成功向 Google 官方换取 Refresh Token！{C_RESET}")
                return rt
            else:
                print(f"{C_RED}未返回 refresh_token: {resp_data}{C_RESET}")
                return ""
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8", errors="ignore")
        print(f"{C_RED}向 Google 换取 Token 失败 ({e.code}): {err_body}{C_RESET}")
        return ""
    except Exception as e:
        print(f"{C_RED}请求异常: {e}{C_RESET}")
        return ""


def set_oauth_credentials():
    print(f"\n{C_BOLD}{C_BLUE}=== 配置 Google Drive OAuth 2.0 凭据 ==={C_RESET}")

    conn = get_db()
    c = conn.cursor()
    c.execute("SELECT addition FROM x_storages LIMIT 1")
    row = c.fetchone()
    conn.close()

    old_add = {}
    if row:
        try:
            old_add = json.loads(row["addition"])
        except Exception:
            pass

    cur_cid = old_add.get("client_id", "")
    cur_sec = old_add.get("client_secret", "")
    cur_rt = old_add.get("refresh_token", "")
    cur_root = old_add.get("root_folder_id", "root")

    print("提示：直接按回车可保留当前显示的默认值。\n")
    cid_prompt = f" [{cur_cid[:12]}...]" if cur_cid else ""
    sec_prompt = f" [{cur_sec[:6]}...]" if cur_sec else ""
    cid = input(f"1. 客户端 ID (Client ID){cid_prompt}: ").strip() or cur_cid
    sec = input(f"2. 客户端密钥 (Client Secret){sec_prompt}: ").strip() or cur_sec

    if not cid or not sec:
        print(f"{C_RED}错误: Client ID 和 Client Secret 不能为空！{C_RESET}\n")
        return

    print("\n请选择 Refresh Token 获取方式：")
    print("  [1] 直接输入/保留已有的 Refresh Token (默认)")
    print("  [2] 生成 Google 官方登录链接，在浏览器登录直接换取新 Refresh Token")
    mode = input("请选择 [1/2，直接回车默认 1]: ").strip() or "1"

    if mode == "2":
        rt = exchange_code_for_refresh_token(cid, sec)
        if not rt:
            return
    else:
        rt_prompt = f" [{cur_rt[:10]}...]" if cur_rt else ""
        rt = input(f"3. 刷新令牌 (Refresh Token){rt_prompt}: ").strip() or cur_rt

    root_id = input(f"4. 根文件夹 ID (Root Folder ID) [{cur_root}]: ").strip() or cur_root

    if not rt:
        print(f"{C_RED}错误: Refresh Token 不能为空！{C_RESET}\n")
        return

    addition_obj = {
        "root_folder_id": root_id,
        "client_id": cid,
        "client_secret": sec,
        "refresh_token": rt,
        "order_by": "",
        "order_direction": "",
        "chunk_size": 5,
        "disable_disk_usage": False
    }

    update_db_storage(addition_obj)
    restart_service()


def update_db_storage(addition_obj):
    conn = get_db()
    c = conn.cursor()
    add_str = json.dumps(addition_obj)
    now = time.strftime("%Y-%m-%d %H:%M:%S")

    c.execute("SELECT id FROM x_storages LIMIT 1")
    row = c.fetchone()
    if row:
        c.execute("""
            UPDATE x_storages 
            SET mount_path = '/', driver = 'GoogleDrive', cache_expiration = 0, addition = ?, modified = ?, status = 'work', disabled = 0
            WHERE id = ?
        """, (add_str, now, row["id"]))
    else:
        c.execute("""
            INSERT INTO x_storages 
            (mount_path, "order", driver, cache_expiration, custom_cache_policies, status, addition, remark, modified, disabled, disable_index, enable_sign, seed_policy, order_by, order_direction, extract_folder, web_proxy, webdav_policy, proxy_range, down_proxy_url, disable_proxy_sign)
            VALUES ('/', 0, 'GoogleDrive', 0, '', 'work', ?, '', ?, 0, 0, 0, 'inherit', '', '', '', 0, 'native_proxy', 0, '', 0)
        """, (add_str, now))

    conn.commit()
    conn.close()
    print(f"{C_GREEN}✓ 数据库云盘配置更新完毕。{C_RESET}")


def test_connection():
    print(f"\n{C_YELLOW}正在测试 Google Drive 读取...{C_RESET}")
    conn = get_db()
    c = conn.cursor()
    c.execute("SELECT value FROM x_setting_items WHERE key = 'token' LIMIT 1")
    row = c.fetchone()
    conn.close()

    token = row["value"] if row else ""
    if not token:
        print(f"{C_RED}获取 Admin Token 失败，请先启动服务。{C_RESET}\n")
        return

    url = "http://127.0.0.1:5244/api/fs/list"
    req_data = json.dumps({"path": "/", "page": 1, "per_page": 5, "refresh": True}).encode("utf-8")
    r = urllib.request.Request(url, data=req_data, headers={
        "Content-Type": "application/json",
        "Authorization": token
    })
    try:
        with urllib.request.urlopen(r, timeout=10) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            if data.get("code") == 200:
                items = [f["name"] for f in data.get("data", {}).get("content", [])]
                print(f"{C_GREEN}✓ 连接测试成功！Google Drive 正常响应。{C_RESET}")
                print(f"  根目录文件样例: {items}\n")
            else:
                print(f"{C_RED}连接测试失败: {data.get('message')}{C_RESET}\n")
    except Exception as e:
        print(f"{C_RED}请求失败: {e}{C_RESET}\n")


def uninstall():
    confirm = input(f"{C_RED}确认彻底卸载 Google Drive 服务与全部数据？[y/N]: {C_RESET}").strip().lower()
    if confirm != "y":
        print("已取消卸载。\n")
        return
    subprocess.run(["systemctl", "stop", SERVICE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(["systemctl", "disable", SERVICE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for p in ["/etc/systemd/system/gwd.service", "/usr/local/bin/gwdctl", "/usr/local/sbin/gwdctl"]:
        if os.path.exists(p) or os.path.islink(p):
            os.remove(p)
    subprocess.run(["systemctl", "daemon-reload"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if os.path.exists(INSTALL_DIR):
        shutil.rmtree(INSTALL_DIR, ignore_errors=True)
    print(f"{C_GREEN}✓ Google Drive 服务已彻底卸载。{C_RESET}")
    sys.exit(0)


def main():
    if len(sys.argv) > 1 and sys.argv[1] in ["uninstall", "remove"]:
        uninstall()
        return

    while True:
        print(f"{C_BOLD}{C_CYAN}===================================================={C_RESET}")
        print(f"{C_BOLD}          Google Drive 终端管理工具                 {C_RESET}")
        print(f"{C_BOLD}{C_CYAN}===================================================={C_RESET}")
        print("  1. 查看当前系统与云盘状态")
        print("  2. 配置/更换谷歌云盘凭据")
        print("  3. 设置/修改登录密码")
        print("  4. 获取/管理 2FA 验证")
        print("  5. 测试云盘连通性")
        print("  6. 重启服务")
        print("  0. 退出")
        print(f"{C_CYAN}----------------------------------------------------{C_RESET}")
        try:
            choice = input("请输入选项编号 [0-6]: ").strip()
        except (EOFError, KeyboardInterrupt):
            print("\n已退出。")
            break

        if choice == "1":
            show_status()
        elif choice == "2":
            set_oauth_credentials()
        elif choice == "3":
            set_admin_account()
        elif choice == "4":
            manage_2fa()
        elif choice == "5":
            test_connection()
        elif choice == "6":
            restart_service()
        elif choice in ["0", "q", "exit"]:
            print("已退出。")
            break
        else:
            print("无效输入，请重新选择。\n")


if __name__ == "__main__":
    main()
