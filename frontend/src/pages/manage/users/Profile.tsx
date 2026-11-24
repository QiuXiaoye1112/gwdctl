import {
  Button,
  Divider,
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
import { Me, setMe, me, setSettings } from "~/store"
import { EmptyResp, PEmptyResp } from "~/types"
import {
  changeToken,
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

  const [savingUsername, saveUsernameReq] = useFetch((): PEmptyResp =>
    r.post("/me/update", {
      username: username().trim(),
      password: "",
    }),
  )
  const [savingPassword, savePasswordReq] = useFetch((): PEmptyResp =>
    r.post("/me/update", {
      username: me().username,
      password: password(),
    }),
  )
  const [cancel2faLoading, cancel2fa] = useFetch((): PEmptyResp =>
    r.post("/auth/2fa/cancel"),
  )

  const timeoutOptions = () => [
    { value: "5", label: t("users.timeout_5m") },
    { value: "10", label: t("users.timeout_10m_default") },
    { value: "15", label: t("users.timeout_15m") },
    { value: "30", label: t("users.timeout_30m") },
    { value: "60", label: t("users.timeout_1h") },
    { value: "120", label: t("users.timeout_2h") },
    { value: "360", label: t("users.timeout_6h") },
    { value: "720", label: t("users.timeout_12h") },
    { value: "1440", label: t("users.timeout_24h") },
    { value: "0", label: t("users.timeout_never") },
    { value: "custom", label: t("users.timeout_custom") },
  ]

  const saveBasicSettings = async () => {
    const nextUsername = username().trim()
    if (!nextUsername) {
      notify.warning(t("users.username_empty"))
      return
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

    if (nextUsername !== me().username) {
      const resp = await saveUsernameReq()
      handleResp(resp, () => {
        changeToken("")
        setMe({} as Me)
        notify.success(t("users.update_profile_success"))
        to(`/@login?redirect=${encodeURIComponent(location.pathname)}`, true)
      })
    } else if (settingSuccess) {
      notify.success(t("users.save_settings_success"))
    }
  }

  const updatePassword = async () => {
    if (!password()) {
      notify.warning(t("users.new_password_empty"))
      return
    }
    if (password() !== confirmPassword()) {
      notify.warning(t("users.confirm_password_not_same"))
      return
    }
    const resp = await savePasswordReq()
    handleResp(resp, () => {
      setPassword("")
      setConfirmPassword("")
      changeToken("")
      setMe({} as Me)
      notify.success(t("users.update_password_success"))
      to(`/@login?redirect=${encodeURIComponent(location.pathname)}`, true)
    })
  }

  const disable2FA = async () => {
    const resp = await cancel2fa()
    handleResp(resp, () => {
      setMe({ ...me(), otp: false })
      notify.success(t("users.cancel_2fa_success"))
    })
  }

  return (
    <VStack w="$full" spacing="$6" alignItems="start">
      <VStack
        as="form"
        autocomplete="off"
        onSubmit={(e: Event) => {
          e.preventDefault()
          saveBasicSettings()
        }}
        w="$full"
        spacing="$4"
        alignItems="start"
      >
        <Heading>{t("users.basic_and_session")}</Heading>
        <SimpleGrid gap="$3" columns={{ "@initial": 1, "@md": 2 }} w="$full">
          <FormControl>
            <FormLabel for="username">{t("users.username")}</FormLabel>
            <Input
              id="username"
              autocomplete="off"
              value={username()}
              onInput={(e) => {
                setUsername(e.currentTarget.value)
              }}
            />
          </FormControl>
          <FormControl>
            <FormLabel for="session-timeout">
              {t("users.session_timeout")}
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
              options={timeoutOptions()}
            />
            <FormHelperText>{t("users.session_timeout_tips")}</FormHelperText>
          </FormControl>
          <Show when={sessionTimeoutOption() === "custom"}>
            <FormControl>
              <FormLabel for="custom-timeout">
                {t("users.custom_timeout")}
              </FormLabel>
              <Input
                id="custom-timeout"
                type="number"
                min={0}
                value={customTimeout()}
                onInput={(e) => setCustomTimeout(e.currentTarget.value)}
                placeholder={t("users.custom_timeout_placeholder")}
              />
              <FormHelperText>{t("users.custom_timeout_tips")}</FormHelperText>
            </FormControl>
          </Show>
        </SimpleGrid>
        <Button
          type="submit"
          loading={savingUsername() || savingTimeout()}
        >
          {t("users.save_settings")}
        </Button>
      </VStack>

      <Divider />

      <VStack
        as="form"
        autocomplete="off"
        onSubmit={(e: Event) => {
          e.preventDefault()
          updatePassword()
        }}
        w="$full"
        spacing="$4"
        alignItems="start"
      >
        <Heading>{t("users.password_and_security")}</Heading>
        <SimpleGrid gap="$3" columns={{ "@initial": 1, "@md": 2 }} w="$full">
          <FormControl>
            <FormLabel for="new-password">{t("users.new_password")}</FormLabel>
            <Input
              id="new-password"
              type="password"
              autocomplete="new-password"
              placeholder={t("users.new_password_placeholder")}
              value={password()}
              onInput={(e) => {
                setPassword(e.currentTarget.value)
              }}
            />
          </FormControl>
          <FormControl>
            <FormLabel for="confirm-new-password">
              {t("users.confirm_new_password")}
            </FormLabel>
            <Input
              id="confirm-new-password"
              type="password"
              autocomplete="new-password"
              placeholder={t("users.confirm_new_password_placeholder")}
              value={confirmPassword()}
              onInput={(e) => {
                setConfirmPassword(e.currentTarget.value)
              }}
            />
          </FormControl>
        </SimpleGrid>
        <HStack spacing="$2">
          <Button type="submit" loading={savingPassword()}>
            {t("users.update_password")}
          </Button>
          <Show
            when={!me().otp}
            fallback={
              <Button
                type="button"
                colorScheme="danger"
                loading={cancel2faLoading()}
                onClick={disable2FA}
              >
                {t("users.cancel_2fa")}
              </Button>
            }
          >
            <Button
              type="button"
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
    </VStack>
  )
}

export default Profile
