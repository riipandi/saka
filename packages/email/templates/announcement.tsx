import { Hr } from 'react-email'
import { CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface AnnouncementData {
  name: string
  topic: string
  title: string
  body: string
}

interface AnnouncementEmailProps {
  logoURL: string
  appName: string
  data: AnnouncementData
}

export const AnnouncementEmail = ({ logoURL, appName, data }: AnnouncementEmailProps) => {
  return (
    <BaseTemplate logoURL={logoURL} appName={appName}>
      <CardHeader title='Announcement' />
      <Hr style={{ marginTop: '16px' }} />
      <Text style={{ marginTop: '18px' }}>Hello {data.name},</Text>

      <Text>
        <strong>{data.title}</strong>
      </Text>

      {data.topic ? (
        <Text>
          Topic: <strong>{data.topic}</strong>
        </Text>
      ) : null}

      <Text>{data.body}</Text>

      <CardFooter>
        <Text size='sm' style={{ marginBottom: '4px' }}>
          You&apos;re receiving this email because you have an account in {appName}. <br />
          You can read the announcement in your notification inbox after signing in.
        </Text>
      </CardFooter>
    </BaseTemplate>
  )
}

AnnouncementEmail.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    // Empty when the announcement named no topic: the template renders its
    // absence.
    topic: '{{.Data.Topic}}',
    title: '{{.Data.Title}}',
    body: '{{.Data.Body}}'
  }
}

AnnouncementEmail.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Hermione Granger',
    topic: 'Maintenance',
    title: 'Scheduled maintenance this weekend',
    body: 'The service will be briefly unavailable on Sunday morning while we upgrade the database.'
  }
}

export default AnnouncementEmail
