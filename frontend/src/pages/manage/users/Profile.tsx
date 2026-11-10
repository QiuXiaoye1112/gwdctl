import {
  Button,
  Checkbox,
  FormControl,
  FormHelperText,
  FormLabel,
  Heading,
  HStack,
  Input,
  SimpleGrid,
  VStack,
} from "@hope-ui/solid"
import { createSignal, Show } from "solid-js"
import { useFetch, useManageTitle, useRouter, useT } from "~/hooks"
import { setMe, me, setSettings } from "~/store"
import { EmptyResp, PEmptyResp } from "~/types"
import {
  handleResp,
  notify,
  r,
  getSessionTimeoutMinutes,
  setSessionTimeoutMinutes,
} from "~/utils"
import { SelectWrapper } from "~/components"

const PRESET_TIMEOUTS = [
  "5",
  "10",
  "15",
  "30",
  "60",
  "120",
  "360",
  "720",
  "1440",
  "0",
]

const Profile = () => {
  const t = useT()
  useManageTitle("manage.sidemenu.profile")
  const { to } = useRouter()
  const [username, setUsername] = createSignal(me().username)
  const [changePassword, setChangePassword] = createSignal(false)
  const [password, setPassword] = createSignal("")
  const [confirmPassword, setConfirmPassword] = createSignal("")
  const currentTimeout = getSessionTimeoutMinutes()
  const [sessionTimeoutOption, setSessionTimeoutOption] = createSignal(
    PRESET_TIMEOUTS.includes(String(currentTimeout))
      ? String(currentTimeout)
      : "custom",
  )
  const [customTimeout, setCustomTimeout] = createSignal(String(currentTimeout))
  const [savingTimeout, setSavingTimeout] = createSignal(false)

  const [loading, save] = useFetch((): PEmptyResp =>
    r.post("/me/update", {
      username: username(),
      password: password(),
    }),
  )
  const [cancel2faLoading, cancel2fa] = useFetch((): PEmptyResp =>
    r.post("/auth/2fa/cancel"),
  )

  const timeoutOptions = [
    {
      value: "5",
      label: localStorage.getItem("lang") === "en" ? "5 Minutes" : "5 分钟",
    },
    {
      value: "10",
      label:
        localStorage.getItem("lang") === "en"
          ? "10 Minutes (Default)"
          : "10 分钟（默认）",
    },
    {
      value: "15",
      label: localStorage.getItem("lang") === "en" ? "15 Minutes" : "15 分钟",
    },
    {
      value: "30",
      label: localStorage.getItem("lang") === "en" ? "30 Minutes" : "30 分钟",
    },
    {
      value: "60",
      label: localStorage.getItem("lang") === "en" ? "1 Hour" : "1 小时",
    },
    {
      value: "120",
      label: localStorage.getItem("lang") === "en" ? "2 Hours" : "2 小时",
    },
    {
      value: "360",
      label: localStorage.getItem("lang") === "en" ? "6 Hours" : "6 小时",
    },
    {
      value: "720",
      label: localStorage.getItem("lang") === "en" ? "12 Hours" : "12 小时",
    },
    {
      value: "1440",
      label: localStorage.getItem("lang") === "en" ? "24 Hours" : "24 小时",
    },
    {
      value: "0",
      label:
        localStorage.getItem("lang") === "en" ? "Never Timeout" : "永不超时",
    },
    {
      value: "custom",
      label:
        localStorage.getItem("lang") === "en"
          ? "Custom Minutes..."
          : "自定义分钟数...",
    },
  ]

  const saveMe = async () => {
    if (changePassword()) {
      if (!password()) {
        notify.warning(
          localStorage.getItem("lang") === "en"
            ? "Please enter a new password"
            : "请输入新密码",
        )
        return
      }
      if (password() !== confirmPassword()) {
        notify.warning(t("users.confirm_password_not_same"))
        return
      }
    }

    const opt = sessionTimeoutOption()
    const targetMinutes =
      opt === "custom"
        ? Math.max(0, parseInt(customTimeout(), 10) || 0)
        : Math.max(0, parseInt(opt, 10) || 0)

    setSessionTimeoutMinutes(targetMinutes)

    setSavingTimeout(true)
    let settingSuccess = true
    try {
      const resp = await r.post<unknown, EmptyResp>("/admin/setting/save", [
        {
          key: "session_timeout_minutes",
          value: String(targetMinutes),
          type: "number",
          group: 4,
          flag: 0,
        },
      ])
      if (resp.code === 200) {
        setSettings({ session_timeout_minutes: String(targetMinutes) })
      } else {
        settingSuccess = false
        notify.error(resp.message)
      }
    } catch (e) {
      settingSuccess = false
      console.error(
        "Failed to persist session_timeout_minutes to backend settings",
        e,
      )
    } finally {
      setSavingTimeout(false)
    }

    const usernameChanged = username() !== me().username
    const passwordChanged = changePassword() && Boolean(password())

    if (usernameChanged || passwordChanged) {
      const resp = await save()
      handleResp(resp, () => {
        setMe({ ...me(), username: username() })
        notify.success(t("users.update_profile_success"))
        to(`/@login?redirect=${encodeURIComponent(location.pathname)}`)
      })
    } else if (settingSuccess) {
      notify.success(
        localStorage.getItem("lang") === "en"
          ? "Profile and session settings saved"
          : "个人资料与会话设置已保存",
      )
    }
  }

  const disable2FA = async () => {
    const resp = await cancel2fa()
    handleResp(resp, () => {
      setMe({ ...me(), otp: false })
      notify.success(t("users.cancel_2fa_success"))
    })
  }

  return (
    <VStack w="$full" spacing="$4" alignItems="start">
      <Heading>{t("users.update_profile")}</Heading>
      <SimpleGrid gap="$2" columns={{ "@initial": 1, "@md": 2 }} w="$full">
        <FormControl>
          <FormLabel for="username">{t("users.change_username")}</FormLabel>
          <Input
            id="username"
            value={username()}
            onInput={(e) => {
              setUsername(e.currentTarget.value)
            }}
          />
        </FormControl>
      </SimpleGrid>
      <SimpleGrid gap="$2" columns={{ "@initial": 1, "@md": 2 }} w="$full">
        <FormControl>
          <FormLabel for="session-timeout">
            {localStorage.getItem("lang") === "en"
              ? "Session Retention Time"
              : "会话保留时间"}
          </FormLabel>
          <SelectWrapper
            w="$full"
            value={sessionTimeoutOption()}
            onChange={(val) => {
              setSessionTimeoutOption(val)
              if (val !== "custom") {
                setCustomTimeout(val)
              }
            }}
            options={timeoutOptions}
          />
          <FormHelperText>
            {localStorage.getItem("lang") === "en"
              ? "Automatically log out after this period of inactivity"
              : "无操作达到此时间后将自动登出以保护账号安全"}
          </FormHelperText>
        </FormControl>
        <Show when={sessionTimeoutOption() === "custom"}>
          <FormControl>
            <FormLabel for="custom-timeout">
              {localStorage.getItem("lang") === "en"
                ? "Custom Time (Minutes)"
                : "自定义时间（分钟）"}
            </FormLabel>
            <Input
              id="custom-timeout"
              type="number"
              min={0}
              value={customTimeout()}
              onInput={(e) => setCustomTimeout(e.currentTarget.value)}
              placeholder={
                localStorage.getItem("lang") === "en" ? "e.g. 15" : "例如 15"
              }
            />
            <FormHelperText>
              {localStorage.getItem("lang") === "en"
                ? "Enter timeout duration in minutes"
                : "请输入超时分钟数"}
            </FormHelperText>
          </FormControl>
        </Show>
      </SimpleGrid>
      <Checkbox
        checked={changePassword()}
        onChange={() => {
          setChangePassword(!changePassword())
          setPassword("")
          setConfirmPassword("")
        }}
      >
        {localStorage.getItem("lang") === "en" ? "Change Password" : "更改密码"}
      </Checkbox>
      <Show when={changePassword()}>
        <SimpleGrid gap="$2" columns={{ "@initial": 1, "@md": 2 }} w="$full">
          <FormControl>
            <FormLabel for="password">{t("users.change_password")}</FormLabel>
            <Input
              id="password"
              type="password"
              autocomplete="new-password"
              placeholder="********"
              value={password()}
              onInput={(e) => {
                setPassword(e.currentTarget.value)
              }}
            />
            <FormHelperText>{t("users.change_password-tips")}</FormHelperText>
          </FormControl>
          <FormControl>
            <FormLabel for="confirm-password">
              {t("users.confirm_password")}
            </FormLabel>
            <Input
              id="confirm-password"
              type="password"
              autocomplete="new-password"
              placeholder="********"
              value={confirmPassword()}
              onInput={(e) => {
                setConfirmPassword(e.currentTarget.value)
              }}
            />
            <FormHelperText>{t("users.confirm_password-tips")}</FormHelperText>
          </FormControl>
        </SimpleGrid>
      </Show>
      <HStack spacing="$2">
        <Button loading={loading() || savingTimeout()} onClick={saveMe}>
          {t("global.save")}
        </Button>
        <Show
          when={!me().otp}
          fallback={
            <Button
              colorScheme="danger"
              loading={cancel2faLoading()}
              onClick={disable2FA}
            >
              {t("users.cancel_2fa")}
            </Button>
          }
        >
          <Button
            colorScheme="accent"
            onClick={() => {
              to("/@manage/2fa")
            }}
          >
            {t("users.enable_2fa")}
          </Button>
        </Show>
      </HStack>
    </VStack>
  )
}

export default Profile
