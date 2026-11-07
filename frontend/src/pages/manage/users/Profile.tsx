import {
  Button,
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
import { setMe, me } from "~/store"
import { PEmptyResp } from "~/types"
import { handleResp, notify, r } from "~/utils"

const Profile = () => {
  const t = useT()
  useManageTitle("manage.sidemenu.profile")
  const { to } = useRouter()
  const [username, setUsername] = createSignal(me().username)
  const [password, setPassword] = createSignal("")
  const [confirmPassword, setConfirmPassword] = createSignal("")
  const [loading, save] = useFetch((): PEmptyResp =>
    r.post("/me/update", {
      username: username(),
      password: password(),
    }),
  )
  const [cancel2faLoading, cancel2fa] = useFetch((): PEmptyResp =>
    r.post("/auth/2fa/cancel"),
  )

  const saveMe = async () => {
    if (password() && password() !== confirmPassword()) {
      notify.warning(t("users.confirm_password_not_same"))
      return
    }
    const resp = await save()
    handleResp(resp, () => {
      setMe({ ...me(), username: username() })
      notify.success(t("users.update_profile_success"))
      to(`/@login?redirect=${encodeURIComponent(location.pathname)}`)
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
    <VStack w="$full" spacing="$4" alignItems="start">
      <Heading>{t("users.update_profile")}</Heading>
      <SimpleGrid gap="$2" columns={{ "@initial": 1, "@md": 2 }}>
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
      <SimpleGrid gap="$2" columns={{ "@initial": 1, "@md": 2 }}>
        <FormControl>
          <FormLabel for="password">{t("users.change_password")}</FormLabel>
          <Input
            id="password"
            type="password"
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
            placeholder="********"
            value={confirmPassword()}
            onInput={(e) => {
              setConfirmPassword(e.currentTarget.value)
            }}
          />
          <FormHelperText>{t("users.confirm_password-tips")}</FormHelperText>
        </FormControl>
      </SimpleGrid>
      <HStack spacing="$2">
        <Button loading={loading()} onClick={saveMe}>
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
